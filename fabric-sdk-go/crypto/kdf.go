package crypto

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

// Argon2idParams are the parameters used to derive a FIF envelope key from a
// passphrase. The default values match fabric-spec-001-v1.0.md's
// envelope_params.kdf_params for envelope_type="passphrase-only".
type Argon2idParams struct {
	// Time is the number of iterations (Argon2 "t_cost"). Default: 3.
	Time uint32
	// Memory is the memory cost in KiB (Argon2 "m_cost"). Default: 65536 (64 MiB).
	Memory uint32
	// Threads is the parallelism factor. Default: 4.
	Threads uint8
	// KeyLen is the derived key length in bytes. Default: KeySize (32).
	KeyLen uint32
}

// DefaultArgon2idParams returns the FIF v1.0 defaults: t=3, m=65536 KiB, p=4, len=32.
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		Time:    3,
		Memory:  65536,
		Threads: 4,
		KeyLen:  KeySize,
	}
}

// DeriveKeyArgon2id derives a symmetric key from a passphrase using Argon2id.
// Used to unlock the FIF passphrase-only envelope.
func DeriveKeyArgon2id(passphrase string, salt []byte, p Argon2idParams) []byte {
	return argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

// DeriveKey derives a 32-byte symmetric key via HKDF-SHA256.
// secret is the input keying material (typically a master key).
// salt MAY be nil; per RFC 5869, a nil salt is replaced with a zero string of HashLen.
// info is the per-purpose context string (e.g. namespace name for per-namespace keys).
func DeriveKey(secret, salt, info []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, secret, salt, info)
	out := make([]byte, KeySize)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("fabric/crypto: hkdf: %w", err)
	}
	return out, nil
}
