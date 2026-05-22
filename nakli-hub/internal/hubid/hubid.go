// Package hubid manages the Hub's own keypair and macaroon root key.
//
// Per hub-spec-001-v1.1.md §"Security posture", the hub holds:
//   - An Ed25519 keypair (for signing freshness metadata, discharge macaroons,
//     peer-to-peer sync auth)
//   - A 32-byte macaroon HMAC root key (for minting and verifying Grants this
//     Hub issues)
//
// SECURITY POSTURE
//
// Both secrets are stored together in hub-identity.json under data_dir at
// mode 0600. Anyone who can read this file can mint arbitrary Grants
// impersonating any principal, sign discharges, complete pair flows —
// so the file MUST stay on-device:
//
//   - DO NOT include data_dir in OS backups that leave the machine
//     (Time Machine to a NAS, iCloud Drive sync, LVM snapshots that
//     ship offsite, etc.).
//   - DO use FileVault / LUKS / BitLocker on the disk.
//   - DO scope the operator account narrowly; the file is readable by
//     anyone with that user's shell.
//
// Load refuses to start when the file mode is not exactly 0600 — a
// minimal defense against accidental world-readability from a botched
// restore or chmod. Set NAKLI_HUB_INSECURE_IDENTITY_MODE=1 to bypass
// (with the obvious risk acceptance).
//
// Full encrypt-at-rest under an operator passphrase
// (Argon2id + XChaCha20-Poly1305, reusing fabric-sdk-go/crypto) is
// deferred to v1.x — tracked in plan/pending.md as P3, because it's a
// passphrase-prompt UX change that breaks every systemd unit, Docker
// entrypoint, and CI fixture that assumes silent startup.
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

// ErrInsecureIdentityMode is returned by Load when the identity file's
// permission bits are not exactly 0600 (and the operator hasn't set
// NAKLI_HUB_INSECURE_IDENTITY_MODE=1 to accept the risk).
var ErrInsecureIdentityMode = errors.New("hubid: identity file permissions must be 0600 (group/other access is forbidden)")

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

// Load reads an identity from path. Returns os.ErrNotExist if path is
// missing. Returns ErrInsecureIdentityMode if the file mode is not
// exactly 0600 — unless NAKLI_HUB_INSECURE_IDENTITY_MODE=1 is set in
// the environment, which the operator can use to explicitly accept
// the risk (e.g., to recover from a backup-restore that reset the
// mode bits).
func Load(path string) (*Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm() != 0o600 && os.Getenv("NAKLI_HUB_INSECURE_IDENTITY_MODE") != "1" {
		return nil, fmt.Errorf("%w: %s has mode 0%o, want 0600", ErrInsecureIdentityMode, path, info.Mode().Perm())
	}
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
