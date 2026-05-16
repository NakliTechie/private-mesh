package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrHistoryConflict is returned when previous_event_hash does not match the
// stream's current head. Maps to fabric-spec error_code "conflict" (HTTP 409).
var ErrHistoryConflict = errors.New("storage: history hash chain conflict")

// HistoryAppendInput is the History-specific shape. The caller has authorized
// the request; storage validates the hash chain.
type HistoryAppendInput struct {
	StreamID            string
	Kind                string
	PayloadCiphertext   []byte
	PayloadMetadata     string
	CausalDependencies  string
	VectorClock         string
	PreviousEventHash   []byte
	AppendedByPrincipal string
	AppendedByGrantID   string
	AppendedByDevice    string
}

// HistoryAppendResult mirrors AppendResult plus the computed event_hash.
type HistoryAppendResult struct {
	EventID        string
	SequenceNumber int64
	EventHash      []byte
	AppendedAt     time.Time
	BlobPath       string
}

// AppendHistoryEvent inserts a history event with hash-chain validation.
// History streams live under namespace "__history" so they don't collide with
// vault streams in the (namespace, stream_id) primary key.
const HistoryNamespace = "__history"

// ComputeHistoryEventHash returns SHA-256 of (prev || event_id || kind ||
// payload_metadata || causal_dependencies) per hub-spec §"History append".
func ComputeHistoryEventHash(prev []byte, eventID, kind, payloadMetadata, causalDeps string) []byte {
	h := sha256.New()
	h.Write(prev)
	h.Write([]byte(eventID))
	h.Write([]byte(kind))
	h.Write([]byte(payloadMetadata))
	h.Write([]byte(causalDeps))
	return h.Sum(nil)
}

// AppendHistoryEvent commits a history append after verifying the hash chain.
// On chain mismatch it returns ErrHistoryConflict (HTTP 409).
func (s *Store) AppendHistoryEvent(ctx context.Context, in HistoryAppendInput, blobPath, eventID string) (*HistoryAppendResult, error) {
	now := s.Now().UTC()
	appendedAt := now.Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Look up current head hash. NULL == "stream does not exist yet"; the
	// caller's previous_event_hash must be empty in that case.
	var (
		currentHead []byte
		streamRows  int
	)
	err = tx.QueryRowContext(ctx, `
        SELECT COALESCE(head_event_hash, x''), event_count FROM streams
        WHERE namespace = ? AND stream_id = ?`,
		HistoryNamespace, in.StreamID,
	).Scan(&currentHead, &streamRows)
	if errors.Is(err, sql.ErrNoRows) {
		// First append. Insert the stream row with head set after the event.
		if len(in.PreviousEventHash) != 0 {
			return nil, ErrHistoryConflict
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO streams (stream_id, namespace, stream_type, created_at, event_count)
            VALUES (?, ?, ?, ?, 0)`,
			in.StreamID, HistoryNamespace, string(StreamHistory), appendedAt); err != nil {
			return nil, fmt.Errorf("AppendHistoryEvent: insert stream: %w", err)
		}
		currentHead = nil
	} else if err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: head lookup: %w", err)
	} else {
		if !bytes.Equal(currentHead, in.PreviousEventHash) {
			return nil, ErrHistoryConflict
		}
	}

	// Compute the next sequence number and new event hash.
	var seq int64
	if err := tx.QueryRowContext(ctx, `
        SELECT COALESCE(MAX(sequence_number), 0) + 1 FROM events
        WHERE namespace = ? AND stream_id = ?`,
		HistoryNamespace, in.StreamID).Scan(&seq); err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: seq: %w", err)
	}
	newHash := ComputeHistoryEventHash(currentHead, eventID, in.Kind, in.PayloadMetadata, in.CausalDependencies)

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO events (
            event_id, namespace, stream_id, stream_type, sequence_number, kind,
            blob_path, payload_size_bytes, payload_metadata, causal_dependencies,
            vector_clock, previous_event_hash, event_hash,
            appended_at, appended_by_principal, appended_by_grant_id, appended_by_device
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, HistoryNamespace, in.StreamID, string(StreamHistory), seq, in.Kind,
		blobPath, int64(len(in.PayloadCiphertext)),
		nullableString(in.PayloadMetadata), nullableString(in.CausalDependencies),
		in.VectorClock, currentHead, newHash,
		appendedAt, in.AppendedByPrincipal, in.AppendedByGrantID, nullableString(in.AppendedByDevice),
	); err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE streams SET head_event_id = ?, head_event_hash = ?, event_count = event_count + 1
        WHERE namespace = ? AND stream_id = ?`,
		eventID, newHash, HistoryNamespace, in.StreamID); err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: update head: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("AppendHistoryEvent: commit: %w", err)
	}
	return &HistoryAppendResult{
		EventID:        eventID,
		SequenceNumber: seq,
		EventHash:      newHash,
		AppendedAt:     now,
		BlobPath:       blobPath,
	}, nil
}

// HistoryVerifyResult is the small return shape of VerifyHistoryChain.
type HistoryVerifyResult struct {
	Verified bool
	Length   int64
	HeadHash []byte
	// BrokenAtEventID identifies where the chain mismatches when Verified=false.
	BrokenAtEventID string
}

// VerifyHistoryChain walks the stream end-to-end and recomputes each event's
// hash. Returns Verified=false at the first mismatch.
func (s *Store) VerifyHistoryChain(ctx context.Context, streamID string) (*HistoryVerifyResult, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT event_id, kind, COALESCE(payload_metadata, ''),
               COALESCE(causal_dependencies, '[]'),
               previous_event_hash, event_hash
        FROM events
        WHERE namespace = ? AND stream_id = ?
        ORDER BY sequence_number ASC`,
		HistoryNamespace, streamID,
	)
	if err != nil {
		return nil, fmt.Errorf("VerifyHistoryChain: %w", err)
	}
	defer rows.Close()
	var (
		expectedPrev []byte
		count        int64
		lastHash     []byte
		broken       string
	)
	for rows.Next() {
		var (
			evID, kind, payloadMeta, causalDeps string
			prevHash, evHash                    []byte
		)
		if err := rows.Scan(&evID, &kind, &payloadMeta, &causalDeps, &prevHash, &evHash); err != nil {
			return nil, fmt.Errorf("VerifyHistoryChain: scan: %w", err)
		}
		if !bytes.Equal(prevHash, expectedPrev) {
			return &HistoryVerifyResult{
				Verified:        false,
				Length:          count,
				HeadHash:        lastHash,
				BrokenAtEventID: evID,
			}, nil
		}
		computed := ComputeHistoryEventHash(prevHash, evID, kind, payloadMeta, causalDeps)
		if !bytes.Equal(computed, evHash) {
			_ = broken
			return &HistoryVerifyResult{
				Verified:        false,
				Length:          count,
				HeadHash:        lastHash,
				BrokenAtEventID: evID,
			}, nil
		}
		expectedPrev = evHash
		lastHash = evHash
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("VerifyHistoryChain: iter: %w", err)
	}
	return &HistoryVerifyResult{Verified: true, Length: count, HeadHash: lastHash}, nil
}
