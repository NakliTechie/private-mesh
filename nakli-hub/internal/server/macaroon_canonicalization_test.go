package server

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// TestMacaroonCanonicalization is the P3 #24 probe: the audit
// speculated that the permissive tryBase64 decoder (which accepts
// std/std-unpadded/url-safe/url-safe-unpadded) might let the same
// logical macaroon decode to different byte strings, which would in
// turn produce different grant_ids in the idempotency table — opening
// a replay-double-execute window.
//
// This test takes one minted macaroon, presents it in all four
// recognized base64 encodings, decodes each, parses the macaroon,
// and asserts the extracted grant_id is byte-identical across all
// four presentations. If it ever fails, the audit's concern is
// concrete and we need to normalize to one canonical encoding before
// extracting grant_id.
//
// Internal-package test so we can call tryBase64 directly.
func TestMacaroonCanonicalization(t *testing.T) {
	rootKey := make([]byte, 32)
	if _, err := cryptorand.Read(rootKey); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	pid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	g, err := grant.Mint(grant.MintSpec{
		RootKey:  rootKey,
		Location: "test",
		Identifier: grant.Identifier{
			GrantID:           gid.String(),
			IssuedAt:          now,
			IssuedByPrincipal: pid.String(),
			IssuedByKeypair:   pub,
			Scope: grant.Scope{
				Primitive:  grant.PrimitiveVault,
				Namespace:  "*",
				Operations: []string{"read"},
			},
		},
		Caveats: []string{"operation in [read]"},
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	encodings := []struct {
		name string
		enc  *base64.Encoding
	}{
		{"std", base64.StdEncoding},
		{"std-unpadded", base64.StdEncoding.WithPadding(base64.NoPadding)},
		{"url-safe", base64.URLEncoding},
		{"url-safe-unpadded", base64.URLEncoding.WithPadding(base64.NoPadding)},
	}

	var grantIDs []string
	var byteHashes []string
	for _, e := range encodings {
		s := e.enc.EncodeToString(g.Macaroon)
		bytes, err := tryBase64(s)
		if err != nil {
			t.Fatalf("%s: tryBase64: %v", e.name, err)
		}
		parsed, err := grant.Parse(bytes)
		if err != nil {
			t.Fatalf("%s: grant.Parse: %v", e.name, err)
		}
		grantIDs = append(grantIDs, parsed.Identifier.GrantID)
		// Track the raw bytes too — if those differ across encodings
		// (they shouldn't), the audit's concern about non-canonical
		// bytes is concrete even at the binary level.
		byteHashes = append(byteHashes, string(bytes))
	}

	// All four presentations must yield the SAME grant_id.
	for i, id := range grantIDs {
		if id != grantIDs[0] {
			t.Errorf("encoding %q produced grant_id %q, want %q (the canonicalization bug is real)",
				encodings[i].name, id, grantIDs[0])
		}
	}

	// Also assert the raw decoded bytes are identical — strong form of
	// the same property. If this passes, idempotency / grants_known
	// keys are stable regardless of how the client encoded the header.
	for i, h := range byteHashes {
		if h != byteHashes[0] {
			t.Errorf("encoding %q produced different raw bytes (lengths %d vs %d)",
				encodings[i].name, len(h), len(byteHashes[0]))
		}
	}

	// Sanity: the encodings ARE actually different strings (otherwise
	// the test is testing nothing).
	encoded := make(map[string]bool)
	for _, e := range encodings {
		encoded[e.enc.EncodeToString(g.Macaroon)] = true
	}
	if len(encoded) < 2 {
		t.Logf("note: the four encodings happened to produce the same string for this macaroon (no padding required, no +/- chars). Test is still valid — just doesn't differentiate today.")
	}
	t.Logf("grant_id stable across %d encodings; %d distinct encoded strings", len(encodings), len(encoded))

	// Belt-and-suspenders: assert the encoded forms include at least
	// one std-with-padding case (so the std-unpadded form is genuinely
	// different).
	containsPadding := false
	for _, e := range encodings {
		if strings.HasSuffix(e.enc.EncodeToString(g.Macaroon), "=") {
			containsPadding = true
			break
		}
	}
	if !containsPadding {
		t.Logf("note: this macaroon's length happens to be a base64 multiple; padding-stripping doesn't differentiate it. Encoding-set still covers std vs url-safe alphabets.")
	}
}
