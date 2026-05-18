package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CratePairingToken is the in-process shape of a crate_pairing_tokens row.
// Schema per Migrations[1] in schema.go. Mirrors the wire payload of a
// CRATE-PAIR-{base64(JSON)} token from crate-pairing-protocol-v1.0.md.
type CratePairingToken struct {
	Secret                 string
	PayloadJSON            string
	BucketID               string
	IdentityPubkey         string
	TransportEndpoint      string
	TransportType          string
	IssuedAt               time.Time
	ExpiresAt              time.Time
	RedeemedAt             *time.Time
	RedeemedByDaemonPubkey string
	DaemonFingerprint      string
	IssuedCapabilityID     string
	CancelledAt            *time.Time
	CreatedAt              time.Time
}

// ErrCratePairingTokenExpired is returned by RedeemCratePairingToken when the
// token's expires_at is in the past.
var ErrCratePairingTokenExpired = errors.New("storage: crate pairing token expired")

// ErrCratePairingTokenAlreadyRedeemed is returned when the token has already
// been redeemed (single-use).
var ErrCratePairingTokenAlreadyRedeemed = errors.New("storage: crate pairing token already redeemed")

// ErrCratePairingTokenCancelled is returned when the token was cancelled by
// the issuing browser before the daemon attempted to redeem it.
var ErrCratePairingTokenCancelled = errors.New("storage: crate pairing token cancelled")

// CreateCratePairingToken inserts a new row. Caller is responsible for
// generating `secret` (32 bytes random, base64url).
func (s *Store) CreateCratePairingToken(ctx context.Context, t CratePairingToken) error {
	if t.Secret == "" {
		return errors.New("CreateCratePairingToken: Secret is empty")
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = s.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO crate_pairing_tokens (
            secret, payload_json, bucket_id, identity_pubkey,
            transport_endpoint, transport_type,
            issued_at, expires_at, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Secret, t.PayloadJSON, t.BucketID, t.IdentityPubkey,
		t.TransportEndpoint, t.TransportType,
		t.IssuedAt.UTC().Format(time.RFC3339Nano),
		t.ExpiresAt.UTC().Format(time.RFC3339Nano),
		t.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("CreateCratePairingToken: %w", err)
	}
	return nil
}

// LookupCratePairingToken returns the token row by secret or ErrNotFound.
func (s *Store) LookupCratePairingToken(ctx context.Context, secret string) (*CratePairingToken, error) {
	var (
		t           CratePairingToken
		issuedAt    string
		expiresAt   string
		createdAt   string
		redeemedAt  sql.NullString
		redeemedBy  sql.NullString
		fingerprint sql.NullString
		capID       sql.NullString
		cancelledAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT secret, payload_json, bucket_id, identity_pubkey,
               transport_endpoint, transport_type,
               issued_at, expires_at, created_at,
               redeemed_at, redeemed_by_daemon_pubkey,
               daemon_fingerprint, issued_capability_id, cancelled_at
        FROM crate_pairing_tokens WHERE secret = ?`,
		secret,
	).Scan(
		&t.Secret, &t.PayloadJSON, &t.BucketID, &t.IdentityPubkey,
		&t.TransportEndpoint, &t.TransportType,
		&issuedAt, &expiresAt, &createdAt,
		&redeemedAt, &redeemedBy, &fingerprint, &capID, &cancelledAt,
	)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	if ts, perr := time.Parse(time.RFC3339Nano, issuedAt); perr == nil {
		t.IssuedAt = ts
	}
	if ts, perr := time.Parse(time.RFC3339Nano, expiresAt); perr == nil {
		t.ExpiresAt = ts
	}
	if ts, perr := time.Parse(time.RFC3339Nano, createdAt); perr == nil {
		t.CreatedAt = ts
	}
	if redeemedAt.Valid && redeemedAt.String != "" {
		if ts, perr := time.Parse(time.RFC3339Nano, redeemedAt.String); perr == nil {
			t.RedeemedAt = &ts
		}
	}
	if redeemedBy.Valid {
		t.RedeemedByDaemonPubkey = redeemedBy.String
	}
	if fingerprint.Valid {
		t.DaemonFingerprint = fingerprint.String
	}
	if capID.Valid {
		t.IssuedCapabilityID = capID.String
	}
	if cancelledAt.Valid && cancelledAt.String != "" {
		if ts, perr := time.Parse(time.RFC3339Nano, cancelledAt.String); perr == nil {
			t.CancelledAt = &ts
		}
	}
	return &t, nil
}

// RedeemCratePairingToken atomically marks a token as redeemed and records
// the daemon's pubkey + fingerprint + issued capability_id. Returns
// ErrCratePairingTokenAlreadyRedeemed on a race (the second caller loses).
// Returns ErrCratePairingTokenExpired / ErrCratePairingTokenCancelled if the
// token is no longer redeemable. Returns ErrNotFound if no row matches.
func (s *Store) RedeemCratePairingToken(
	ctx context.Context,
	secret string,
	daemonPubkey string,
	fingerprintJSON string,
	capabilityID string,
) (*CratePairingToken, error) {
	// Look up first to disambiguate "not found" / "expired" / "cancelled" /
	// "already redeemed" — each maps to a different HTTP status code.
	existing, err := s.LookupCratePairingToken(ctx, secret)
	if err != nil {
		return nil, err
	}
	if existing.CancelledAt != nil {
		return nil, ErrCratePairingTokenCancelled
	}
	if existing.RedeemedAt != nil {
		return nil, ErrCratePairingTokenAlreadyRedeemed
	}
	now := s.Now().UTC()
	if now.After(existing.ExpiresAt) {
		return nil, ErrCratePairingTokenExpired
	}
	// Atomic mark: WHERE redeemed_at IS NULL guards against a concurrent
	// redeem call sneaking through between the lookup above and this UPDATE.
	res, err := s.db.ExecContext(ctx, `
        UPDATE crate_pairing_tokens
        SET redeemed_at = ?,
            redeemed_by_daemon_pubkey = ?,
            daemon_fingerprint = ?,
            issued_capability_id = ?
        WHERE secret = ? AND redeemed_at IS NULL AND cancelled_at IS NULL`,
		now.Format(time.RFC3339Nano), daemonPubkey, fingerprintJSON, capabilityID, secret,
	)
	if err != nil {
		return nil, fmt.Errorf("RedeemCratePairingToken: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Race: someone redeemed (or cancelled) between Lookup and Update.
		return nil, ErrCratePairingTokenAlreadyRedeemed
	}
	// Re-read to return the canonical post-redemption row.
	return s.LookupCratePairingToken(ctx, secret)
}

// CancelCratePairingToken marks an unredeemed token as cancelled by the
// issuing browser. Returns ErrCratePairingTokenAlreadyRedeemed if the token
// was already redeemed (cancellation after redemption is a no-op error;
// callers can revoke the issued capability instead).
func (s *Store) CancelCratePairingToken(ctx context.Context, secret string) error {
	existing, err := s.LookupCratePairingToken(ctx, secret)
	if err != nil {
		return err
	}
	if existing.RedeemedAt != nil {
		return ErrCratePairingTokenAlreadyRedeemed
	}
	if existing.CancelledAt != nil {
		// Already cancelled — idempotent.
		return nil
	}
	now := s.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
        UPDATE crate_pairing_tokens
        SET cancelled_at = ?
        WHERE secret = ? AND cancelled_at IS NULL AND redeemed_at IS NULL`,
		now.Format(time.RFC3339Nano), secret,
	)
	if err != nil {
		return fmt.Errorf("CancelCratePairingToken: %w", err)
	}
	return nil
}
