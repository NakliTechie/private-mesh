// Package hubid manages the Hub's own keypair and macaroon root key.
//
// Per hub-spec-001-v1.1.md §"Security posture", the hub holds:
//   - An Ed25519 keypair (for signing freshness metadata, discharge macaroons,
//     peer-to-peer sync auth)
//   - A 32-byte macaroon HMAC root key (for minting and verifying Grants this
//     Hub issues)
//
// Both are stored together in hub-identity.json under data_dir. Phase 2a does
// not encrypt-at-rest; v1.x will (per the spec's `hub.identity.passphrase`).
package hubid

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/oklog/ulid/v2"
)

// MacaroonRootKeySize is the byte length of the HMAC root key.
const MacaroonRootKeySize = 32

// Identity is the Hub's persistent identity.
type Identity struct {
	HubID            string `json:"hub_id"`
	PublicKey        []byte `json:"public_key"`
	PrivateKey       []byte `json:"private_key"`
	MacaroonRootKey  []byte `json:"macaroon_root_key"`
	CreatedAt        string `json:"created_at"`
}

// Generate returns a fresh Hub identity. Caller saves it via Save.
func Generate(now func() string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("hubid.Generate: ed25519: %w", err)
	}
	mrk := make([]byte, MacaroonRootKeySize)
	if _, err := rand.Read(mrk); err != nil {
		return nil, fmt.Errorf("hubid.Generate: macaroon root key: %w", err)
	}
	id, err := ulidString()
	if err != nil {
		return nil, err
	}
	return &Identity{
		HubID:           id,
		PublicKey:       pub,
		PrivateKey:      priv,
		MacaroonRootKey: mrk,
		CreatedAt:       now(),
	}, nil
}

// Save writes the identity to path with restrictive permissions (0600).
func (id *Identity) Save(path string) error {
	if err := id.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Load reads an identity from path. Returns os.ErrNotExist if path is missing.
func Load(path string) (*Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, fmt.Errorf("hubid.Load: parse %s: %w", path, err)
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return &id, nil
}

// Validate checks invariants on the identity.
func (id *Identity) Validate() error {
	if id.HubID == "" {
		return errors.New("hubid: HubID is empty")
	}
	if len(id.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("hubid: PublicKey length %d, want %d", len(id.PublicKey), ed25519.PublicKeySize)
	}
	if len(id.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("hubid: PrivateKey length %d, want %d", len(id.PrivateKey), ed25519.PrivateKeySize)
	}
	if len(id.MacaroonRootKey) != MacaroonRootKeySize {
		return fmt.Errorf("hubid: MacaroonRootKey length %d, want %d", len(id.MacaroonRootKey), MacaroonRootKeySize)
	}
	return nil
}

func ulidString() (string, error) {
	id, err := ulid.New(ulid.Now(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("hubid: ulid: %w", err)
	}
	return id.String(), nil
}
