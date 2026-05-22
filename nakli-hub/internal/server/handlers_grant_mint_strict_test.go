package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
)

// TestGrantMint_StrictRequiresParent covers P2 #14: with the flag on,
// /grant/mint must reject requests that omit parent_grant_macaroon —
// otherwise the holder of any wildcard `grant:mint` Grant can mint
// arbitrary scopes, bypassing the spec's "only attenuate" promise.
func TestGrantMint_StrictRequiresParent(t *testing.T) {
	h := newHubFixture(t, func(c *config.Config) {
		c.Auth.StrictMintRequiresParent = true
	})
	mintGrant := h.mintGrantWithScope(t, grant.PrimitiveGrant, "*", []string{"mint"}, nil)

	body := map[string]any{
		"recipient_principal_id": "01JTESTRECIPIENT0000000001",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "anything-i-want",
			"operations": []string{"read", "write"},
		},
		// no parent_grant_macaroon — pre-fix this was the attenuation bypass.
	}
	status, respBody := h.do(t, "POST", "/fabric/v1/grant/mint", body, map[string]string{
		"X-Fabric-Grant":           mintGrant,
		"X-Fabric-Idempotency-Key": "test-strict-mint-no-parent",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("strict + no parent: got %d, want 400; body=%s", status, respBody)
	}
	var env errorEnv
	_ = json.Unmarshal(respBody, &env)
	if env.Error.Code != "bad_request" {
		t.Errorf("error code: %q, want bad_request", env.Error.Code)
	}
}

// TestGrantMint_DefaultAllowsRootMint asserts the flag defaults OFF —
// existing JS SDK browser apps that root-mint via /grant/mint keep
// working without explicit operator opt-out.
func TestGrantMint_DefaultAllowsRootMint(t *testing.T) {
	h := newHubFixture(t) // default: StrictMintRequiresParent=false
	mintGrant := h.mintGrantWithScope(t, grant.PrimitiveGrant, "*", []string{"mint"}, nil)

	body := map[string]any{
		"recipient_principal_id": "01JTESTRECIPIENT0000000002",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "anything",
			"operations": []string{"read"},
		},
	}
	status, respBody := h.do(t, "POST", "/fabric/v1/grant/mint", body, map[string]string{
		"X-Fabric-Grant":           mintGrant,
		"X-Fabric-Idempotency-Key": "test-default-mint-no-parent",
	})
	if status != http.StatusOK {
		t.Fatalf("default flag + no parent: got %d, want 200 (compat preserved); body=%s", status, respBody)
	}
}

// TestGrantMint_StrictAcceptsWithParent: with the flag on, supplying a
// valid parent still works (delegation path is untouched).
func TestGrantMint_StrictAcceptsWithParent(t *testing.T) {
	h := newHubFixture(t, func(c *config.Config) {
		c.Auth.StrictMintRequiresParent = true
	})
	mintGrant := h.mintGrantWithScope(t, grant.PrimitiveGrant, "*", []string{"mint"}, nil)

	// Step 1: create a parent macaroon out-of-band (mintGrantWithScope
	// uses the SDK directly, mirroring the operator's `nakli-cli grant
	// mint` flow). This is the SDK-direct path the new flag funnels
	// root-mint into.
	parentB64 := h.mintGrantWithScope(t, grant.PrimitiveVault, "list", []string{"read", "write"}, nil)

	body := map[string]any{
		"recipient_principal_id": "01JTESTRECIPIENT0000000003",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read"},
		},
		"caveats":                []string{},
		"parent_grant_macaroon":  parentB64,
	}
	status, respBody := h.do(t, "POST", "/fabric/v1/grant/mint", body, map[string]string{
		"X-Fabric-Grant":           mintGrant,
		"X-Fabric-Idempotency-Key": "test-strict-mint-with-parent",
	})
	if status != http.StatusOK {
		t.Fatalf("strict + valid parent: got %d, want 200; body=%s", status, respBody)
	}
}
