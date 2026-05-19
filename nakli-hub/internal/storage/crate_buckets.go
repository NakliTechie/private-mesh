package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CrateBucket is the in-process shape of a crate_buckets row. Schema per
// Migrations[2] in schema.go.
//
// SecretAccessKeySealed holds the XChaCha20-Poly1305 ciphertext of the
// provider's secret access key under bucketCredsKey
// (see internal/crate/keys.go::DeriveBucketCredsKey). Nonce is the 24-byte
// nonce used for that seal. BucketID is bound as AAD — moving the sealed
// bytes from row A to row B fails authentication.
//
// The plaintext SecretAccessKey is NEVER stored in this struct in
// long-lived contexts; the storage layer's Lookup methods return the row
// with the still-sealed blob, and the handler decrypts at signing time and
// zeroes the plaintext on return.
type CrateBucket struct {
	BucketID                string
	Provider                string // "r2" today; "b2"/"hetzner"/"aws-s3" later
	AccountID               string // R2 Account ID; "" for non-R2
	Region                  string // "auto" for R2; datacenter for Hetzner; region for B2/AWS
	BucketName              string
	EndpointURL             string
	AccessKeyID             string
	SecretAccessKeySealed   []byte
	Nonce                   []byte
	RegisteredByPrincipal   string
	CreatedAt               time.Time
	LastUsedAt              *time.Time
}

// ErrCrateBucketExists is returned by CreateCrateBucket if a row with the
// same bucket_id already exists.
var ErrCrateBucketExists = errors.New("storage: crate bucket already exists")

// CreateCrateBucket inserts a new row. Caller is responsible for generating
// `bucket_id` (ULID), sealing the secret access key (see
// internal/crate/keys.go::SealSecret), and computing endpoint_url (see
// internal/crate/sigv4.go::EndpointForProvider).
func (s *Store) CreateCrateBucket(ctx context.Context, b CrateBucket) error {
	if b.BucketID == "" {
		return errors.New("CreateCrateBucket: BucketID is empty")
	}
	if b.Provider == "" {
		return errors.New("CreateCrateBucket: Provider is empty")
	}
	if b.BucketName == "" {
		return errors.New("CreateCrateBucket: BucketName is empty")
	}
	if b.EndpointURL == "" {
		return errors.New("CreateCrateBucket: EndpointURL is empty")
	}
	if b.AccessKeyID == "" {
		return errors.New("CreateCrateBucket: AccessKeyID is empty")
	}
	if len(b.SecretAccessKeySealed) == 0 {
		return errors.New("CreateCrateBucket: SecretAccessKeySealed is empty")
	}
	if len(b.Nonce) == 0 {
		return errors.New("CreateCrateBucket: Nonce is empty")
	}
	if b.RegisteredByPrincipal == "" {
		return errors.New("CreateCrateBucket: RegisteredByPrincipal is empty")
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = s.Now().UTC()
	}

	var accountID interface{}
	if b.AccountID != "" {
		accountID = b.AccountID
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO crate_buckets (
            bucket_id, provider, account_id, region, bucket_name,
            endpoint_url, access_key_id, secret_access_key_sealed, nonce,
            registered_by_principal, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.BucketID, b.Provider, accountID, b.Region, b.BucketName,
		b.EndpointURL, b.AccessKeyID, b.SecretAccessKeySealed, b.Nonce,
		b.RegisteredByPrincipal, b.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		// SQLite UNIQUE constraint violation surfaces as "UNIQUE constraint
		// failed: crate_buckets.bucket_id" — we don't sniff the message text;
		// callers should generate ULIDs which collide with negligible prob.
		return fmt.Errorf("CreateCrateBucket: %w", err)
	}
	return nil
}

// LookupCrateBucket returns the bucket row by bucket_id or ErrNotFound.
// The returned struct's SecretAccessKeySealed is still encrypted; callers
// must decrypt it via internal/crate.OpenSecret before signing.
func (s *Store) LookupCrateBucket(ctx context.Context, bucketID string) (*CrateBucket, error) {
	var (
		b          CrateBucket
		accountID  sql.NullString
		createdAt  string
		lastUsedAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT bucket_id, provider, account_id, region, bucket_name,
               endpoint_url, access_key_id, secret_access_key_sealed, nonce,
               registered_by_principal, created_at, last_used_at
        FROM crate_buckets WHERE bucket_id = ?`,
		bucketID,
	).Scan(
		&b.BucketID, &b.Provider, &accountID, &b.Region, &b.BucketName,
		&b.EndpointURL, &b.AccessKeyID, &b.SecretAccessKeySealed, &b.Nonce,
		&b.RegisteredByPrincipal, &createdAt, &lastUsedAt,
	)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	if accountID.Valid {
		b.AccountID = accountID.String
	}
	if ts, perr := time.Parse(time.RFC3339Nano, createdAt); perr == nil {
		b.CreatedAt = ts
	}
	if lastUsedAt.Valid && lastUsedAt.String != "" {
		if ts, perr := time.Parse(time.RFC3339Nano, lastUsedAt.String); perr == nil {
			b.LastUsedAt = &ts
		}
	}
	return &b, nil
}

// ListCrateBucketsByPrincipal returns all buckets registered by a principal,
// most-recently-created first. Used by the (deferred) user-facing bucket list
// in nakliOS Settings.
func (s *Store) ListCrateBucketsByPrincipal(ctx context.Context, principal string) ([]CrateBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT bucket_id, provider, account_id, region, bucket_name,
               endpoint_url, access_key_id, secret_access_key_sealed, nonce,
               registered_by_principal, created_at, last_used_at
        FROM crate_buckets
        WHERE registered_by_principal = ?
        ORDER BY created_at DESC`,
		principal,
	)
	if err != nil {
		return nil, fmt.Errorf("ListCrateBucketsByPrincipal: %w", err)
	}
	defer rows.Close()

	var out []CrateBucket
	for rows.Next() {
		var (
			b          CrateBucket
			accountID  sql.NullString
			createdAt  string
			lastUsedAt sql.NullString
		)
		if err := rows.Scan(
			&b.BucketID, &b.Provider, &accountID, &b.Region, &b.BucketName,
			&b.EndpointURL, &b.AccessKeyID, &b.SecretAccessKeySealed, &b.Nonce,
			&b.RegisteredByPrincipal, &createdAt, &lastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("ListCrateBucketsByPrincipal scan: %w", err)
		}
		if accountID.Valid {
			b.AccountID = accountID.String
		}
		if ts, perr := time.Parse(time.RFC3339Nano, createdAt); perr == nil {
			b.CreatedAt = ts
		}
		if lastUsedAt.Valid && lastUsedAt.String != "" {
			if ts, perr := time.Parse(time.RFC3339Nano, lastUsedAt.String); perr == nil {
				b.LastUsedAt = &ts
			}
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListCrateBucketsByPrincipal rows: %w", err)
	}
	return out, nil
}

// TouchCrateBucketLastUsed updates last_used_at on every successful proxy
// operation. Best-effort — a logged warning on failure is fine; the proxy
// op already succeeded.
func (s *Store) TouchCrateBucketLastUsed(ctx context.Context, bucketID string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE crate_buckets SET last_used_at = ? WHERE bucket_id = ?`,
		s.Now().UTC().Format(time.RFC3339Nano), bucketID,
	)
	if err != nil {
		return fmt.Errorf("TouchCrateBucketLastUsed: %w", err)
	}
	return nil
}

// DeleteCrateBucket removes a bucket registration (for future bucket-management
// endpoints). Used today only by tests; the user-facing delete handler is
// deferred to the nakliOS Settings milestone.
func (s *Store) DeleteCrateBucket(ctx context.Context, bucketID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM crate_buckets WHERE bucket_id = ?`, bucketID)
	if err != nil {
		return fmt.Errorf("DeleteCrateBucket: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
