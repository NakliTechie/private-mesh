package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// SyncEvent is the on-the-wire shape of an event flowing between peers via
// /sync/push and /sync/pull. Mirrors AppendInput + ReadEvent without the
// blob-on-disk indirection — sync uses the in-memory ciphertext bytes.
type SyncEvent struct {
	Namespace           string
	StreamID            string
	StreamType          string
	EventID             string
	Kind                string
	SequenceNumber      int64
	PayloadCiphertext   []byte
	PayloadMetadata     string
	CausalDependencies  string
	VectorClock         string
	PreviousEventHash   []byte
	EventHash           []byte
	AppendedAt          time.Time
	AppendedByPrincipal string
	AppendedByGrantID   string
}

// SyncPull returns events with rowid > sinceRowID up to limit, plus the next
// cursor and a `more` flag. Cursor is the SQLite rowid of the last returned
// row; clients store + replay it for the next call. Empty `since` means
// "from the beginning".
func (s *Store) SyncPull(ctx context.Context, since string, limit int) ([]SyncEvent, string, bool, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	sinceRowID := int64(0)
	if since != "" {
		v, err := strconv.ParseInt(since, 10, 64)
		if err != nil {
			return nil, "", false, fmt.Errorf("SyncPull: invalid cursor %q: %w", since, err)
		}
		sinceRowID = v
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT rowid, namespace, stream_id, stream_type, event_id, kind, sequence_number,
               blob_path,
               COALESCE(payload_metadata, ''), COALESCE(causal_dependencies, '[]'),
               vector_clock, previous_event_hash, event_hash,
               appended_at, appended_by_principal, appended_by_grant_id
        FROM events
        WHERE rowid > ?
        ORDER BY rowid ASC
        LIMIT ?`,
		sinceRowID, limit+1,
	)
	if err != nil {
		return nil, "", false, fmt.Errorf("SyncPull: query: %w", err)
	}
	defer rows.Close()
	var (
		out       []SyncEvent
		nextRowID int64
	)
	for rows.Next() {
		var (
			rowID      int64
			ev         SyncEvent
			blobPath   string
			appendedAt string
		)
		if err := rows.Scan(
			&rowID, &ev.Namespace, &ev.StreamID, &ev.StreamType, &ev.EventID,
			&ev.Kind, &ev.SequenceNumber,
			&blobPath,
			&ev.PayloadMetadata, &ev.CausalDependencies,
			&ev.VectorClock, &ev.PreviousEventHash, &ev.EventHash,
			&appendedAt, &ev.AppendedByPrincipal, &ev.AppendedByGrantID,
		); err != nil {
			return nil, "", false, fmt.Errorf("SyncPull: scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, appendedAt); err == nil {
			ev.AppendedAt = t
		}
		ciphertext, err := s.ReadBlob(ev.Namespace, ev.EventID)
		if err != nil {
			return nil, "", false, fmt.Errorf("SyncPull: load blob %s: %w", ev.EventID, err)
		}
		ev.PayloadCiphertext = ciphertext
		out = append(out, ev)
		nextRowID = rowID
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, fmt.Errorf("SyncPull: iter: %w", err)
	}
	more := false
	if len(out) > limit {
		out = out[:limit]
		more = true
		// reset to the rowid of the truncated last row
		nextRowID = 0
		_ = s.db.QueryRowContext(ctx, `SELECT rowid FROM events WHERE event_id = ?`, out[len(out)-1].EventID).Scan(&nextRowID)
	}
	cursor := ""
	if nextRowID > 0 {
		cursor = strconv.FormatInt(nextRowID, 10)
	} else if since != "" {
		cursor = since
	}
	return out, cursor, more, nil
}

// ErrAlreadyPresent is returned by IngestEvent when the event_id is already
// in storage. Callers treat this as a skip, not an error.
var ErrAlreadyPresent = errors.New("storage: event already present")

// SyncIngestInput is the Store-level shape of a peer-pushed event. Caller
// has already written the blob to disk; BlobPath points at it. PayloadSize
// is the byte count of the ciphertext.
type SyncIngestInput struct {
	EventID             string
	Namespace           string
	StreamID            string
	StreamType          string
	Kind                string
	BlobPath            string
	PayloadSize         int64
	PayloadMetadata     string
	CausalDependencies  string
	VectorClock         string
	PreviousEventHash   []byte
	EventHash           []byte
	AppendedAt          time.Time
	AppendedByPrincipal string
	AppendedByGrantID   string
}

// IngestEvent stores a peer-pushed event. The sequence_number is computed
// locally; ordering follows arrival order, not the sender's order. Returns
// ErrAlreadyPresent when an event with the same event_id already exists.
func (s *Store) IngestEvent(ctx context.Context, in SyncIngestInput) error {
	if in.EventID == "" || in.Namespace == "" || in.StreamID == "" {
		return errors.New("IngestEvent: event_id, namespace, stream_id required")
	}
	if in.AppendedAt.IsZero() {
		in.AppendedAt = s.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("IngestEvent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM events WHERE event_id = ?`, in.EventID).Scan(&existing); err != nil {
		return fmt.Errorf("IngestEvent: existing check: %w", err)
	}
	if existing > 0 {
		return ErrAlreadyPresent
	}

	var streamRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM streams WHERE namespace = ? AND stream_id = ?`,
		in.Namespace, in.StreamID).Scan(&streamRows); err != nil {
		return fmt.Errorf("IngestEvent: stream lookup: %w", err)
	}
	if streamRows == 0 {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO streams (stream_id, namespace, stream_type, created_at, event_count)
            VALUES (?, ?, ?, ?, 0)`,
			in.StreamID, in.Namespace, in.StreamType, in.AppendedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("IngestEvent: insert stream: %w", err)
		}
	}

	var seq int64
	if err := tx.QueryRowContext(ctx, `
        SELECT COALESCE(MAX(sequence_number), 0) + 1 FROM events
        WHERE namespace = ? AND stream_id = ?`,
		in.Namespace, in.StreamID).Scan(&seq); err != nil {
		return fmt.Errorf("IngestEvent: seq: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO events (
            event_id, namespace, stream_id, stream_type, sequence_number, kind,
            blob_path, payload_size_bytes, payload_metadata, causal_dependencies,
            vector_clock, previous_event_hash, event_hash,
            appended_at, appended_by_principal, appended_by_grant_id
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.EventID, in.Namespace, in.StreamID, in.StreamType, seq, in.Kind,
		in.BlobPath, in.PayloadSize, nullableString(in.PayloadMetadata), nullableString(in.CausalDependencies),
		in.VectorClock, in.PreviousEventHash, in.EventHash,
		in.AppendedAt.UTC().Format(time.RFC3339Nano), in.AppendedByPrincipal, in.AppendedByGrantID,
	); err != nil {
		return fmt.Errorf("IngestEvent: insert event: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
        UPDATE streams SET head_event_id = ?, head_event_hash = ?, event_count = event_count + 1
        WHERE namespace = ? AND stream_id = ?`,
		in.EventID, in.EventHash, in.Namespace, in.StreamID); err != nil {
		return fmt.Errorf("IngestEvent: update head: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("IngestEvent: commit: %w", err)
	}
	return nil
}

// Compile-time guard against sql.ErrNoRows drift.
var _ = sql.ErrNoRows
