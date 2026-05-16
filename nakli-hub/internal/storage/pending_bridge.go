package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PendingBridge is the in-process shape of a pending_bridge row.
type PendingBridge struct {
	PendingID            string
	GrantID              string
	Adapter              string
	Operation            string
	ParamsJSON           string
	RequestedByPrincipal string
	RequestedAt          time.Time
	ApprovedAt           *time.Time
	RejectedAt           *time.Time
	RejectedReason       string
}

// InsertPendingBridge persists a new pending_bridge row. Idempotent on
// PendingID (re-insert with the same id is a no-op).
func (s *Store) InsertPendingBridge(ctx context.Context, p PendingBridge) error {
	if p.PendingID == "" {
		return errors.New("InsertPendingBridge: PendingID is empty")
	}
	if p.RequestedAt.IsZero() {
		p.RequestedAt = s.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO pending_bridge (
            pending_id, grant_id, adapter, operation, params,
            requested_by_principal, requested_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (pending_id) DO NOTHING`,
		p.PendingID, p.GrantID, p.Adapter, p.Operation, p.ParamsJSON,
		p.RequestedByPrincipal,
		p.RequestedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("InsertPendingBridge: %w", err)
	}
	return nil
}

// GetPendingBridge looks up a pending row by id; ErrNotFound when absent.
func (s *Store) GetPendingBridge(ctx context.Context, pendingID string) (*PendingBridge, error) {
	var (
		p          PendingBridge
		requested  string
		approved   string
		rejected   string
		reason     string
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT pending_id, grant_id, adapter, operation, params,
               requested_by_principal, requested_at,
               COALESCE(approved_at, ''), COALESCE(rejected_at, ''), COALESCE(rejected_reason, '')
        FROM pending_bridge WHERE pending_id = ?`,
		pendingID,
	).Scan(&p.PendingID, &p.GrantID, &p.Adapter, &p.Operation, &p.ParamsJSON,
		&p.RequestedByPrincipal, &requested, &approved, &rejected, &reason)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	if t, err := time.Parse(time.RFC3339Nano, requested); err == nil {
		p.RequestedAt = t
	}
	if approved != "" {
		if t, err := time.Parse(time.RFC3339Nano, approved); err == nil {
			p.ApprovedAt = &t
		}
	}
	if rejected != "" {
		if t, err := time.Parse(time.RFC3339Nano, rejected); err == nil {
			p.RejectedAt = &t
		}
	}
	p.RejectedReason = reason
	return &p, nil
}
