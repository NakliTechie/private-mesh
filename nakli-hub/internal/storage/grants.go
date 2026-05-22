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

// MarkGrantRevoked records the Grant as revoked. If grants_known has no row
// for this grant_id (because the Grant was minted out of band rather than via
// /grant/mint), a minimal row is inserted so IsGrantRevoked returns true on
// subsequent lookups. Idempotent.
func (s *Store) MarkGrantRevoked(ctx context.Context, grantID, revocationEventID string) error {
	now := s.nowRFC3339()
	res, err := s.db.ExecContext(ctx, `
        UPDATE grants_known SET revoked_at = ?, revocation_event_id = ?
        WHERE grant_id = ? AND revoked_at IS NULL`,
		now, revocationEventID, grantID,
	)
	if err != nil {
		return fmt.Errorf("MarkGrantRevoked: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No row updated — either already revoked (no-op), or the Grant was
		// never tracked in grants_known. Insert a stub row marking it revoked
		// so future lookups see the revocation.
		_, err = s.db.ExecContext(ctx, `
            INSERT INTO grants_known (
                grant_id, issued_by_principal, recipient_principal, parent_grant_id,
                scope, caveats, issued_at, expires_at, revoked_at, revocation_event_id
            ) VALUES (?, '', '', NULL, '{}', '[]', ?, ?, ?, ?)
            ON CONFLICT (grant_id) DO NOTHING`,
			grantID, now, now, now, revocationEventID,
		)
		if err != nil {
			return fmt.Errorf("MarkGrantRevoked: insert stub: %w", err)
		}
	}
	return nil
}

// GetKnownGrant returns the grants_known row for grantID. The boolean is
// false (with nil error) when the grant has never been seen — callers
// MUST treat this as "unknown ownership" and reject any operation that
// requires verifying the issuer/recipient.
func (s *Store) GetKnownGrant(ctx context.Context, grantID string) (KnownGrant, bool, error) {
	var (
		g          KnownGrant
		issuedAt   string
		expiresAt  string
		revokedAt  string
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT grant_id, issued_by_principal, recipient_principal,
               COALESCE(parent_grant_id, ''), scope, caveats,
               issued_at, expires_at, COALESCE(revoked_at, ''),
               COALESCE(revocation_event_id, '')
        FROM grants_known WHERE grant_id = ?`,
		grantID,
	).Scan(
		&g.GrantID, &g.IssuedByPrincipal, &g.RecipientPrincipal,
		&g.ParentGrantID, &g.ScopeJSON, &g.CaveatsJSON,
		&issuedAt, &expiresAt, &revokedAt, &g.RevocationEventID,
	)
	if errors.Is(err, ErrNotFound) || isNoRows(err) {
		return KnownGrant{}, false, nil
	}
	if err != nil {
		return KnownGrant{}, false, fmt.Errorf("GetKnownGrant: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, issuedAt); perr == nil {
		g.IssuedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, expiresAt); perr == nil {
		g.ExpiresAt = t
	}
	if revokedAt != "" {
		if t, perr := time.Parse(time.RFC3339Nano, revokedAt); perr == nil {
			g.RevokedAt = &t
		}
	}
	return g, true, nil
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
