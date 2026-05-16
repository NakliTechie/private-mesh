package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// KnownGrant is the in-process shape of a grants_known row.
type KnownGrant struct {
	GrantID            string
	IssuedByPrincipal  string
	RecipientPrincipal string
	ParentGrantID      string
	ScopeJSON          string
	CaveatsJSON        string
	IssuedAt           time.Time
	ExpiresAt          time.Time
	RevokedAt          *time.Time
	RevocationEventID  string
}

// RememberGrant inserts a grants_known row. Used for audit and to mark Grants
// the Hub has seen so revocation events can be cross-referenced.
func (s *Store) RememberGrant(ctx context.Context, g KnownGrant) error {
	if g.GrantID == "" {
		return errors.New("RememberGrant: GrantID is empty")
	}
	if g.IssuedAt.IsZero() {
		g.IssuedAt = s.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO grants_known (
            grant_id, issued_by_principal, recipient_principal, parent_grant_id,
            scope, caveats, issued_at, expires_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (grant_id) DO NOTHING`,
		g.GrantID, g.IssuedByPrincipal, g.RecipientPrincipal,
		nullableString(g.ParentGrantID),
		g.ScopeJSON, g.CaveatsJSON,
		g.IssuedAt.UTC().Format(time.RFC3339Nano), g.ExpiresAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("RememberGrant: %w", err)
	}
	return nil
}

// MarkGrantRevoked updates grants_known with revocation metadata. Idempotent.
func (s *Store) MarkGrantRevoked(ctx context.Context, grantID, revocationEventID string) error {
	res, err := s.db.ExecContext(ctx, `
        UPDATE grants_known SET revoked_at = ?, revocation_event_id = ?
        WHERE grant_id = ? AND revoked_at IS NULL`,
		s.nowRFC3339(), revocationEventID, grantID,
	)
	if err != nil {
		return fmt.Errorf("MarkGrantRevoked: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either already revoked or not seen — both acceptable for v1.0
		// since the wire-side history stream is the authoritative record.
	}
	return nil
}

// IsGrantRevoked reports whether a grant has been recorded as revoked.
func (s *Store) IsGrantRevoked(ctx context.Context, grantID string) (bool, error) {
	var revoked string
	err := s.db.QueryRowContext(ctx, `
        SELECT COALESCE(revoked_at, '') FROM grants_known WHERE grant_id = ?`,
		grantID,
	).Scan(&revoked)
	if errors.Is(err, ErrNotFound) || isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return revoked != "", nil
}

func isNoRows(err error) bool {
	return err != nil && err.Error() == "sql: no rows in result set"
}
