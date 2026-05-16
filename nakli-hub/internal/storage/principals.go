package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Principal is the in-process shape of a principals table row.
type Principal struct {
	PrincipalID         string
	PrincipalType       string
	PublicKey           []byte
	ParentPrincipalID   string
	DisplayName         string
	CreatedAt           time.Time
	RetiredAt           *time.Time
	RetirementEventID   string
}

// UpsertPrincipal inserts a principals row or updates the display name if the
// row already exists. Idempotent.
func (s *Store) UpsertPrincipal(ctx context.Context, p Principal) error {
	if p.PrincipalID == "" {
		return errors.New("UpsertPrincipal: PrincipalID is empty")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = s.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO principals (principal_id, principal_type, public_key, parent_principal_id, display_name, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT (principal_id) DO UPDATE SET display_name = excluded.display_name`,
		p.PrincipalID, p.PrincipalType, p.PublicKey,
		nullableString(p.ParentPrincipalID), nullableString(p.DisplayName),
		p.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("UpsertPrincipal: %w", err)
	}
	return nil
}

// GetPrincipal returns the principal by id, or ErrNotFound. Accepts the
// `<ulid>@<fabric-id>` form per forward-compat hook 5 (v2.0 federation) — the
// `@<fabric-id>` suffix is stripped before lookup.
func (s *Store) GetPrincipal(ctx context.Context, principalID string) (*Principal, error) {
	id := stripFabricSuffix(principalID)
	var (
		p          Principal
		createdAt  string
		retiredAt  string
		retirement string
		parent     string
		display    string
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT principal_id, principal_type, public_key,
               COALESCE(parent_principal_id, ''), COALESCE(display_name, ''),
               created_at,
               COALESCE(retired_at, ''), COALESCE(retirement_event_id, '')
        FROM principals WHERE principal_id = ?`,
		id,
	).Scan(&p.PrincipalID, &p.PrincipalType, &p.PublicKey, &parent, &display, &createdAt, &retiredAt, &retirement)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	p.ParentPrincipalID = parent
	p.DisplayName = display
	p.RetirementEventID = retirement
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	if retiredAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, retiredAt); err == nil {
			p.RetiredAt = &t
		}
	}
	return &p, nil
}

// stripFabricSuffix returns the bare ULID portion of a principal id, dropping
// any "@<fabric-id>" suffix v2.0 federation may add (forward-compat hook 5).
func stripFabricSuffix(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '@' {
			return id[:i]
		}
	}
	return id
}

// PrincipalCounts returns counts grouped by principal_type. Used by /health.
func (s *Store) PrincipalCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT principal_type, COUNT(1) FROM principals GROUP BY principal_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var t string
		var n int64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}
