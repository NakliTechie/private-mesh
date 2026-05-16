package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PairingToken is the in-process shape of a pairing_tokens row.
type PairingToken struct {
	Token                  string
	NumericCode            string
	InitiatedByPrincipal   string
	InitiatedAt            time.Time
	ExpiresAt              time.Time
	CompletedAt            *time.Time
	CompletedByDevice      string
	FIFIntegrityCommitment []byte
}

// ErrPairingTokenExpired is returned by CompletePairing when the token's
// expires_at is in the past.
var ErrPairingTokenExpired = errors.New("storage: pairing token expired")

// ErrPairingTokenUsed is returned when the token has already been completed.
var ErrPairingTokenUsed = errors.New("storage: pairing token already used")

// CreatePairingToken inserts a row and returns the persisted PairingToken.
func (s *Store) CreatePairingToken(ctx context.Context, p PairingToken) error {
	if p.InitiatedAt.IsZero() {
		p.InitiatedAt = s.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO pairing_tokens (
            token, numeric_code, initiated_by_principal, initiated_at, expires_at, fif_integrity_commitment
        ) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Token, nullableString(p.NumericCode), p.InitiatedByPrincipal,
		p.InitiatedAt.UTC().Format(time.RFC3339Nano), p.ExpiresAt.UTC().Format(time.RFC3339Nano),
		p.FIFIntegrityCommitment,
	)
	if err != nil {
		return fmt.Errorf("CreatePairingToken: %w", err)
	}
	return nil
}

// LookupPairingToken returns the token row by token or ErrNotFound.
func (s *Store) LookupPairingToken(ctx context.Context, token string) (*PairingToken, error) {
	var (
		p             PairingToken
		numericCode   string
		initiatedAt   string
		expiresAt     string
		completedAt   string
		completedDev  string
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT token, COALESCE(numeric_code, ''), initiated_by_principal,
               initiated_at, expires_at,
               COALESCE(completed_at, ''), COALESCE(completed_by_device, ''),
               fif_integrity_commitment
        FROM pairing_tokens WHERE token = ?`,
		token,
	).Scan(&p.Token, &numericCode, &p.InitiatedByPrincipal,
		&initiatedAt, &expiresAt, &completedAt, &completedDev, &p.FIFIntegrityCommitment)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	p.NumericCode = numericCode
	if t, err := time.Parse(time.RFC3339Nano, initiatedAt); err == nil {
		p.InitiatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, expiresAt); err == nil {
		p.ExpiresAt = t
	}
	if completedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, completedAt); err == nil {
			p.CompletedAt = &t
		}
	}
	p.CompletedByDevice = completedDev
	return &p, nil
}

// CompletePairing marks a pairing token used by the given device. Idempotent
// in the sense that a re-call by the SAME device returns no error; a different
// device gets ErrPairingTokenUsed.
func (s *Store) CompletePairing(ctx context.Context, token, deviceID string) error {
	pt, err := s.LookupPairingToken(ctx, token)
	if err != nil {
		return err
	}
	now := s.Now().UTC()
	if now.After(pt.ExpiresAt) {
		return ErrPairingTokenExpired
	}
	if pt.CompletedAt != nil {
		if pt.CompletedByDevice == deviceID {
			return nil
		}
		return ErrPairingTokenUsed
	}
	_, err = s.db.ExecContext(ctx, `
        UPDATE pairing_tokens
        SET completed_at = ?, completed_by_device = ?
        WHERE token = ? AND completed_at IS NULL`,
		now.Format(time.RFC3339Nano), deviceID, token,
	)
	if err != nil {
		return fmt.Errorf("CompletePairing: %w", err)
	}
	return nil
}
