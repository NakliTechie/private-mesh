package hubid_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
)

// TestLoadRejectsLooseFileMode covers the audit fix (P2 #15 minimum):
// hub-identity.json contains the macaroon HMAC root key + ed25519
// private key. Anyone who can read it can impersonate the Hub. If the
// file mode is ever wider than 0600 (botched restore, careless chmod,
// directory copied across users), the audit recommends failing closed.
func TestLoadRejectsLooseFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub-identity.json")
	id, err := hubid.Generate(func() string { return "2026-05-22T12:00:00Z" })
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path); err != nil {
		t.Fatal(err)
	}

	// Save() writes at 0600 → Load() should succeed.
	if _, err := hubid.Load(path); err != nil {
		t.Fatalf("default 0600 load failed: %v", err)
	}

	// Now widen the mode and expect a refusal.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hubid.Load(path); !errors.Is(err, hubid.ErrInsecureIdentityMode) {
		t.Fatalf("expected ErrInsecureIdentityMode for 0644, got %v", err)
	}

	// Group-readable (0640) is also refused.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := hubid.Load(path); !errors.Is(err, hubid.ErrInsecureIdentityMode) {
		t.Fatalf("expected ErrInsecureIdentityMode for 0640, got %v", err)
	}
}

// TestLoadEnvOverride covers the escape hatch: operators who
// explicitly accept the risk (e.g., backup restore in a container
// that reset the mode) can set NAKLI_HUB_INSECURE_IDENTITY_MODE=1.
func TestLoadEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub-identity.json")
	id, err := hubid.Generate(func() string { return "2026-05-22T12:00:00Z" })
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NAKLI_HUB_INSECURE_IDENTITY_MODE", "1")
	if _, err := hubid.Load(path); err != nil {
		t.Fatalf("override should permit loose mode, got %v", err)
	}
}
