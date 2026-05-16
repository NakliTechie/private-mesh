// Package crypto provides the cryptographic primitives used by the Private Mesh
// fabric SDK: XChaCha20-Poly1305 AEAD for payload and FIF body encryption, plus
// the KDFs (Argon2id, HKDF-SHA256) defined in fabric-spec-001-v1.0.md.
//
// The primitives here are thin wrappers over golang.org/x/crypto. They exist to
// give the rest of the SDK a stable, fabric-shaped API and to centralize the
// "always XChaCha (NewX), always 32-byte keys, always 24-byte nonces" choices
// so they cannot drift.
package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// KeySize is the symmetric key length used everywhere in the fabric (32 bytes).
const KeySize = chacha20poly1305.KeySize

// NonceSize is the XChaCha20-Poly1305 nonce length (24 bytes).
const NonceSize = chacha20poly1305.NonceSizeX

// TagSize is the Poly1305 authentication tag length (16 bytes).
const TagSize = 16

// SaltSize is the FIF envelope salt length (16 bytes per fabric-spec).
const SaltSize = 16

// ErrInvalidKeySize is returned by Seal/Open when the key is not KeySize bytes.
var ErrInvalidKeySize = errors.New("fabric/crypto: key must be 32 bytes")

// ErrInvalidNonceSize is returned by Seal/Open when the nonce is not NonceSize bytes.
var ErrInvalidNonceSize = errors.New("fabric/crypto: nonce must be 24 bytes")

// Seal encrypts plaintext with XChaCha20-Poly1305. The returned slice contains
// the ciphertext followed by the 16-byte Poly1305 tag. aad is authenticated
// but not encrypted; pass nil if there is no AAD.
func Seal(key, nonce, plaintext, aad []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	if len(nonce) != NonceSize {
		return nil, ErrInvalidNonceSize
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("fabric/crypto: aead init: %w", err)
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// Open decrypts and authenticates ciphertext produced by Seal. The ciphertext
// slice MUST include the 16-byte Poly1305 tag appended by Seal. Returns the
// plaintext or an error if authentication fails.
func Open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	if len(nonce) != NonceSize {
		return nil, ErrInvalidNonceSize
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("fabric/crypto: aead init: %w", err)
	}
	return aead.Open(nil, nonce, ciphertext, aad)
}

// RandomBytes returns n cryptographically-random bytes.
func RandomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("fabric/crypto: random: %w", err)
	}
	return buf, nil
}

// RandomNonce returns 24 fresh random bytes suitable for an XChaCha20-Poly1305 nonce.
func RandomNonce() ([]byte, error) { return RandomBytes(NonceSize) }

// RandomSalt returns 16 fresh random bytes suitable for an Argon2id salt.
func RandomSalt() ([]byte, error) { return RandomBytes(SaltSize) }
