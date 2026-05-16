package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// IdempotencyOutcome describes what the idempotency middleware should do.
type IdempotencyOutcome int

const (
	// IdempotencyFresh means no prior record; the request should proceed normally
	// and the handler must call PutIdempotency on success.
	IdempotencyFresh IdempotencyOutcome = iota
	// IdempotencyReplay means a prior request with the same (key, grant_id, payload_hash)
	// succeeded; the stored response should be returned with HTTP 200.
	IdempotencyReplay
	// IdempotencyConflict means a prior request with the same (key, grant_id)
	// but a different payload_hash exists; return HTTP 409.
	IdempotencyConflict
)

// LookupIdempotencyResult is the small return shape of LookupIdempotency.
type LookupIdempotencyResult struct {
	Outcome        IdempotencyOutcome
	ResponseStatus int
	ResponseBody   []byte
}

// HashPayload returns the SHA-256 of body, the form used for idempotency
// payload comparison.
func HashPayload(body []byte) []byte {
	sum := sha256.Sum256(body)
	return sum[:]
}

// LookupIdempotency returns the appropriate outcome for an incoming key/payload
// pair. It does NOT extend the record.
func (s *Store) LookupIdempotency(ctx context.Context, key, grantID string, payloadHash []byte) (*LookupIdempotencyResult, error) {
	if key == "" || grantID == "" {
		return nil, errors.New("LookupIdempotency: empty key or grant id")
	}
	var (
		storedHash   []byte
		responseSt   int
		responseBody []byte
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT payload_hash, response_status, COALESCE(response_body, x'')
        FROM idempotency WHERE key = ? AND grant_id = ?`,
		key, grantID,
	).Scan(&storedHash, &responseSt, &responseBody)
	if errors.Is(err, sql.ErrNoRows) {
		return &LookupIdempotencyResult{Outcome: IdempotencyFresh}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("LookupIdempotency: %w", err)
	}
	if !bytesEqual(storedHash, payloadHash) {
		return &LookupIdempotencyResult{Outcome: IdempotencyConflict}, nil
	}
	return &LookupIdempotencyResult{
		Outcome:        IdempotencyReplay,
		ResponseStatus: responseSt,
		ResponseBody:   responseBody,
	}, nil
}

// PutIdempotency stores the response so future replays return the same body.
// retentionSeconds is the lifetime; spec minimum is 86400 (24h).
func (s *Store) PutIdempotency(ctx context.Context, key, grantID, endpoint string, payloadHash []byte, status int, body []byte, retentionSeconds int64) error {
	if retentionSeconds <= 0 {
		retentionSeconds = 86400
	}
	expiresAt := s.Now().UTC().Add(time.Duration(retentionSeconds) * time.Second).Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO idempotency (key, grant_id, endpoint, payload_hash, response_status, response_body, expires_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, grantID, endpoint, payloadHash, status, body, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("PutIdempotency: %w", err)
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
