package server_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/conformance"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// TestConformanceSuite drives the 32-test suite from fabric-sdk-go/conformance
// against an in-process Hub fixture. M3 gate: 32/32 passing.
func TestConformanceSuite(t *testing.T) {
	h := newHubFixture(t)

	// Test 26 needs a configured peer that the Hub cannot reach. The Hub
	// reports `degraded:true` when any peer URL fails the probe.
	h.srv.SetPeerProbeURLs([]string{"http://127.0.0.1:1/unreachable"})

	// M5.5 conformance tests 12 / 13 / 14 / 31 hit /bridge/call. Install a
	// registry with the conformance-test noop adapter so those calls reach
	// a known no-op dispatch target.
	reg := bridge.NewRegistry(nil)
	reg.MustRegister(bridge.NoopAdapter{})
	h.srv.SetBridgeRegistry(reg)

	// Test 30 needs a retired principal whose id matches the prep hook.
	prep := conformance.DefaultPrep()
	ctx := context.Background()
	pub, _, _ := ed25519.GenerateKey(cryptorand.Reader)
	if err := h.store.UpsertPrincipal(ctx, storage.Principal{
		PrincipalID:   prep.RetiredAgentID,
		PrincipalType: "agent",
		PublicKey:     pub,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertPrincipal: %v", err)
	}
	if err := h.store.RetirePrincipal(ctx, prep.RetiredAgentID, "conformance-test-retirement"); err != nil {
		t.Fatalf("RetirePrincipal: %v", err)
	}

	results := conformance.RunAll(conformance.Config{
		Target:          h.ts.URL,
		MacaroonRootKey: h.id.MacaroonRootKey,
		PrincipalID:     "01J0CONFORMANCERUNNER00000",
	})

	if !results.AllPassed() {
		buf := &bytes.Buffer{}
		results.PrintTable(buf)
		t.Fatalf("\nconformance suite did not reach 32/32:\n%s", buf.String())
	}
	if len(results.Tests) != 32 {
		t.Fatalf("expected 32 tests, got %d", len(results.Tests))
	}
}
