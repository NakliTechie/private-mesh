package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StreamType is the kind of stream a row in the `streams` table holds.
type StreamType string

const (
	StreamVault   StreamType = "vault"
	StreamHistory StreamType = "history"
)

// AppendInput is the Store-level shape of a Vault/History append, after the
// server has parsed the request body and validated authorization.
type AppendInput struct {
	Namespace           string
	StreamID            string
	StreamType          StreamType
	Kind                string
	PayloadCiphertext   []byte
	PayloadMetadata     string // JSON string ("" if absent)
	CausalDependencies  string // JSON array string ("[]" if absent)
	VectorClock         string // JSON object string ("{}" if absent)
	AppendedByPrincipal string
	AppendedByGrantID   string
	AppendedByDevice    string
	// For history streams:
	PreviousEventHash []byte // nil for vault
	EventHash         []byte // nil for vault
}

// AppendResult is the small response shape returned by AppendEvent.
type AppendResult struct {
	EventID        string
	SequenceNumber int64
	AppendedAt     time.Time
	BlobPath       string
}

// AppendEvent inserts a row into events and updates the stream head atomically.
// Caller has already written the blob; blobPath is the on-disk path.
//
// SequenceNumber is computed inside the transaction as MAX(sequence_number)+1
// for the (namespace, stream_id). This avoids races without external locking.
func (s *Store) AppendEvent(ctx context.Context, in AppendInput, blobPath, eventID string) (*AppendResult, error) {
	now := s.Now().UTC()
	appendedAt := now.Format(time.RFC3339Nano)
	var (
		seq        int64
		blobSize   = int64(len(in.PayloadCiphertext))
		streamRows int
	)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("AppendEvent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Ensure the stream row exists.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM streams WHERE namespace = ? AND stream_id = ?`,
		in.Namespace, in.StreamID).Scan(&streamRows); err != nil {
		return nil, fmt.Errorf("AppendEvent: stream lookup: %w", err)
	}
	if streamRows == 0 {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO streams (stream_id, namespace, stream_type, created_at, event_count)
            VALUES (?, ?, ?, ?, 0)`,
			in.StreamID, in.Namespace, string(in.StreamType), appendedAt); err != nil {
			return nil, fmt.Errorf("AppendEvent: insert stream: %w", err)
		}
	}

	// Compute next sequence number.
	if err := tx.QueryRowContext(ctx, `
        SELECT COALESCE(MAX(sequence_number), 0) + 1 FROM events
        WHERE namespace = ? AND stream_id = ?`,
		in.Namespace, in.StreamID).Scan(&seq); err != nil {
		return nil, fmt.Errorf("AppendEvent: seq: %w", err)
	}

	// Insert the event.
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO events (
            event_id, namespace, stream_id, stream_type, sequence_number, kind,
            blob_path, payload_size_bytes, payload_metadata, causal_dependencies,
            vector_clock, previous_event_hash, event_hash,
            appended_at, appended_by_principal, appended_by_grant_id, appended_by_device
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, in.Namespace, in.StreamID, string(in.StreamType), seq, in.Kind,
		blobPath, blobSize, nullableString(in.PayloadMetadata), nullableString(in.CausalDependencies),
		in.VectorClock, in.PreviousEventHash, in.EventHash,
		appendedAt, in.AppendedByPrincipal, in.AppendedByGrantID, nullableString(in.AppendedByDevice),
	); err != nil {
		return nil, fmt.Errorf("AppendEvent: insert event: %w", err)
	}

	// Update stream head.
	if _, err := tx.ExecContext(ctx, `
        UPDATE streams SET head_event_id = ?, head_event_hash = ?, event_count = event_count + 1
        WHERE namespace = ? AND stream_id = ?`,
		eventID, in.EventHash, in.Namespace, in.StreamID); err != nil {
		return nil, fmt.Errorf("AppendEvent: update head: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("AppendEvent: commit: %w", err)
	}
	return &AppendResult{
		EventID:        eventID,
		SequenceNumber: seq,
		AppendedAt:     now,
		BlobPath:       blobPath,
	}, nil
}

// ReadOptions narrows a stream read.
type ReadOptions struct {
	SinceEventID string
	Limit        int
}

// ReadEvent is the Store-level representation returned to the server.
type ReadEvent struct {
	EventID             string
	SequenceNumber      int64
	Kind                string
	PayloadCiphertext   []byte
	PayloadMetadata     string
	CausalDependencies  string
	VectorClock         string
	PreviousEventHash   []byte
	EventHash           []byte
	AppendedAt          time.Time
	AppendedByPrincipal string
	AppendedByDevice    string
}

// ReadStream returns events from (namespace, stream_id) in ascending sequence
// order, optionally starting after SinceEventID. The blob payload is read from
// disk for each row.
func (s *Store) ReadStream(ctx context.Context, namespace, streamID string, opts ReadOptions) ([]*ReadEvent, bool, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var sinceSeq int64
	if opts.SinceEventID != "" {
		if err := s.db.QueryRowContext(ctx, `
            SELECT sequence_number FROM events WHERE namespace = ? AND stream_id = ? AND event_id = ?`,
			namespace, streamID, opts.SinceEventID).Scan(&sinceSeq); err != nil {
			return nil, false, wrapNotFound(err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `
        SELECT event_id, sequence_number, kind, blob_path,
               COALESCE(payload_metadata, ''), COALESCE(causal_dependencies, '[]'),
               vector_clock, previous_event_hash, event_hash,
               appended_at, appended_by_principal, COALESCE(appended_by_device, '')
        FROM events
        WHERE namespace = ? AND stream_id = ? AND sequence_number > ?
        ORDER BY sequence_number ASC
        LIMIT ?`,
		namespace, streamID, sinceSeq, limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("ReadStream: query: %w", err)
	}
	defer rows.Close()

	out := make([]*ReadEvent, 0, limit)
	for rows.Next() {
		var (
			ev          ReadEvent
			blobPath    string
			appendedAt  string
			prevHash    sql.NullString // BLOB is fine but we just want NULL-safety
		)
		_ = prevHash // unused since we read into []byte below
		var prevHashBytes, eventHashBytes []byte
		if err := rows.Scan(
			&ev.EventID, &ev.SequenceNumber, &ev.Kind, &blobPath,
			&ev.PayloadMetadata, &ev.CausalDependencies,
			&ev.VectorClock, &prevHashBytes, &eventHashBytes,
			&appendedAt, &ev.AppendedByPrincipal, &ev.AppendedByDevice,
		); err != nil {
			return nil, false, fmt.Errorf("ReadStream: scan: %w", err)
		}
		ev.PreviousEventHash = prevHashBytes
		ev.EventHash = eventHashBytes
		if t, err := time.Parse(time.RFC3339Nano, appendedAt); err == nil {
			ev.AppendedAt = t
		}
		ciphertext, err := s.ReadBlob(namespace, ev.EventID)
		if err != nil {
			return nil, false, fmt.Errorf("ReadStream: load blob %s: %w", ev.EventID, err)
		}
		ev.PayloadCiphertext = ciphertext
		out = append(out, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("ReadStream: iter: %w", err)
	}
	more := false
	if len(out) > limit {
		out = out[:limit]
		more = true
	}
	return out, more, nil
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
