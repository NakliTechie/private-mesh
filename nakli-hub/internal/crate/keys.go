// Package crate implements the Hub-side bucket-proxy: per-bucket credential
// storage (encrypted-at-rest), server-side sig-v4 signing, and the streaming
// HTTP handlers that let the daemon use its capability against real S3-API
// buckets without ever holding the R2 secret key.
//
// crate-agent/docs/specs/crate-daemon-handoff-v1.0.md §"Hub as bucket-proxy"
// + crate/docs/specs/crate-vision-and-roadmap-v1.0.md lines 91–104 motivate
// this. The daemon authenticates with its capability (issued at Unit C
// pairing-redeem); the Hub holds the sig-v4 creds for the bucket the
// capability is scoped to.
package crate

import (
	"errors"
	"fmt"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/crypto"
)

// bucketCredsKeySalt is the HKDF salt used to derive bucketCredsKey from the
// Hub's macaroon root key. Stable per the encryption-at-rest design — rotating
// this requires re-encrypting every row in crate_buckets, which v1 does NOT
// support. Treat as a compile-time constant.
const bucketCredsKeySalt = "crate-buckets"

// bucketCredsKeyInfo is the HKDF info string. Bumping this (to "v2", etc.)
// would deliberately invalidate all existing sealed rows — the migration
// would need to re-seal. Stable for v1.
const bucketCredsKeyInfo = "v1"

// DeriveBucketCredsKey returns the 32-byte symmetric key used to seal R2 secret
// access keys at rest. It is HKDF-SHA256(macaroon_root_key, salt="crate-buckets",
// info="v1") — derived deterministically from the Hub's existing macaroon root
// (no new key material in hub-identity.json). Rotating the macaroon root key
// also rotates this; that's an acceptable v1 story since rotation isn't
// supported yet anyway.
//
// The caller MUST keep the returned key in memory only; never persist it.
func DeriveBucketCredsKey(macaroonRootKey []byte) ([]byte, error) {
	if len(macaroonRootKey) != crypto.KeySize {
		return nil, fmt.Errorf("crate: macaroon root key length %d, want %d",
			len(macaroonRootKey), crypto.KeySize)
	}
	return crypto.DeriveKey(macaroonRootKey, []byte(bucketCredsKeySalt), []byte(bucketCredsKeyInfo))
}

// ErrSealEmpty is returned when SealSecret is called with an empty plaintext.
// Empty secret access keys are always a bug — the registration handler
// validates non-empty before reaching this layer; this is defence-in-depth.
var ErrSealEmpty = errors.New("crate: refusing to seal empty plaintext")

// SealSecret encrypts a secret access key with bucketCredsKey + a fresh random
// 24-byte nonce. Returns (ciphertext, nonce) — both are stored in crate_buckets
// (ciphertext in secret_access_key_sealed, nonce in nonce). The plaintext is
// authenticated against the bucket_id as AAD so a row swap (move sealed bytes
// from row A to row B) fails authentication.
//
// The fabric-sdk-go crypto layer uses XChaCha20-Poly1305; the 24-byte nonce
// gives birthday-bound safety even with millions of rows.
func SealSecret(key []byte, plaintext []byte, bucketID string) (ciphertext, nonce []byte, err error) {
	if len(plaintext) == 0 {
		return nil, nil, ErrSealEmpty
	}
	if bucketID == "" {
		return nil, nil, errors.New("crate: SealSecret: empty bucketID (AAD required)")
	}
	nonce, err = crypto.RandomNonce()
	if err != nil {
		return nil, nil, fmt.Errorf("crate: nonce: %w", err)
	}
	ciphertext, err = crypto.Seal(key, nonce, plaintext, []byte(bucketID))
	if err != nil {
		return nil, nil, fmt.Errorf("crate: seal: %w", err)
	}
	return ciphertext, nonce, nil
}

// OpenSecret decrypts a sealed secret access key. The bucketID MUST match the
// one used at SealSecret time — it's bound as AAD, so a row swap fails here.
// Returns ErrInvalidCiphertext (via the underlying crypto.Open) when
// authentication fails.
func OpenSecret(key, ciphertext, nonce []byte, bucketID string) ([]byte, error) {
	if bucketID == "" {
		return nil, errors.New("crate: OpenSecret: empty bucketID (AAD required)")
	}
	plaintext, err := crypto.Open(key, nonce, ciphertext, []byte(bucketID))
	if err != nil {
		return nil, fmt.Errorf("crate: open: %w", err)
	}
	return plaintext, nil
}
