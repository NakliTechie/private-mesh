package grant

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustRootKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := cryptorand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func sampleSpec(t *testing.T, rootKey []byte) MintSpec {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return MintSpec{
		RootKey:  rootKey,
		Location: "https://hub.bhai.example",
		Identifier: Identifier{
			GrantID:           "01JFXAMPLETESTGRANT00001",
			IssuedAt:          time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
			IssuedByPrincipal: "01JFXAMPLEHUMAN0000000001",
			IssuedByKeypair:   pub,
			Scope: Scope{
				Primitive:  PrimitiveVault,
				Namespace:  "list",
				Operations: []string{"read", "write"},
			},
		},
		Caveats: []string{
			"time < 2026-06-15T18:00:00Z",
			"operation in [read, write]",
		},
	}
}

func TestMintAndVerifyRoundTrip(t *testing.T) {
	rk := mustRootKey(t)
	g, err := Mint(sampleSpec(t, rk))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(g.Macaroon) == 0 {
		t.Fatal("Macaroon bytes empty")
	}
	if g.GrantID == "" {
		t.Fatal("GrantID missing")
	}

	if err := VerifySignature(g.Macaroon, rk, AlwaysSatisfied); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestMintAndParsePreservesIdentifier(t *testing.T) {
	rk := mustRootKey(t)
	spec := sampleSpec(t, rk)
	g, err := Mint(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(g.Macaroon)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Identifier.GrantID != spec.Identifier.GrantID {
		t.Errorf("GrantID: got %q, want %q", parsed.Identifier.GrantID, spec.Identifier.GrantID)
	}
	if parsed.Identifier.Scope.Primitive != spec.Identifier.Scope.Primitive {
		t.Errorf("Primitive: got %q, want %q", parsed.Identifier.Scope.Primitive, spec.Identifier.Scope.Primitive)
	}
	if !bytes.Equal(parsed.Identifier.IssuedByKeypair, spec.Identifier.IssuedByKeypair) {
		t.Error("IssuedByKeypair mismatch")
	}
	if len(parsed.Caveats) != len(spec.Caveats) {
		t.Fatalf("caveat count: got %d, want %d", len(parsed.Caveats), len(spec.Caveats))
	}
	for i, want := range spec.Caveats {
		if parsed.Caveats[i] != want {
			t.Errorf("caveat[%d]: got %q, want %q", i, parsed.Caveats[i], want)
		}
	}
}

func TestVerifyWrongKeyFails(t *testing.T) {
	rk1 := mustRootKey(t)
	rk2 := mustRootKey(t)
	g, err := Mint(sampleSpec(t, rk1))
	if err != nil {
		t.Fatal(err)
	}
	err = VerifySignature(g.Macaroon, rk2, AlwaysSatisfied)
	if err == nil {
		t.Fatal("VerifySignature with wrong key should fail")
	}
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("errors.Is(err, ErrSignatureInvalid) should be true; got %v", err)
	}
}

func TestVerifyTamperedCaveatFails(t *testing.T) {
	rk := mustRootKey(t)
	g, err := Mint(sampleSpec(t, rk))
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte mid-payload. Locating exactly which byte is fiddly across
	// libmacaroon's binary encoding, so just touch the middle of the buffer
	// and assert the verify rejects it.
	g.Macaroon[len(g.Macaroon)/2] ^= 0x01
	if err := VerifySignature(g.Macaroon, rk, AlwaysSatisfied); err == nil {
		t.Fatal("VerifySignature on tampered macaroon should fail")
	}
}

func TestCheckFuncCanReject(t *testing.T) {
	rk := mustRootKey(t)
	g, err := Mint(sampleSpec(t, rk))
	if err != nil {
		t.Fatal(err)
	}
	rejectAll := func(c string) error { return errors.New("rejected: " + c) }
	err = VerifySignature(g.Macaroon, rk, rejectAll)
	if err == nil {
		t.Fatal("VerifySignature with rejecting CheckFunc should fail")
	}
	if !strings.Contains(err.Error(), "rejected:") {
		t.Errorf("expected rejection in error chain, got %v", err)
	}
}
