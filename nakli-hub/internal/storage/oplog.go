package storage

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// LogOperation appends one row to operation_log.
//
// Per the v1.0 forward-compat hook: operation_log entries are retained for
// at least 90 days. The Hub does NOT aggressively prune; that's the operator's
// choice via a future `nakli-hub gc` command.
func (s *Store) LogOperation(ctx context.Context, grantID, principal, endpoint string, status int, durationMs int64, errorCode string) error {
	opID, err := ulid.New(ulid.Now(), rand.Reader)
	if err != nil {
		return fmt.Errorf("LogOperation: ulid: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO operation_log (op_id, timestamp, grant_id, principal, endpoint, status, duration_ms, error_code)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		opID.String(), s.nowRFC3339(),
		nullableString(grantID), nullableString(principal),
		endpoint, status, durationMs, nullableString(errorCode),
	)
	if err != nil {
		return fmt.Errorf("LogOperation: %w", err)
	}
	return nil
}

// OperationLogCount returns the number of operation_log rows. Used for tests.
func (s *Store) OperationLogCount(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM operation_log`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
