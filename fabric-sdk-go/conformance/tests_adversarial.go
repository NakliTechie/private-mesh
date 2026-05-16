package conformance

import (
	"encoding/json"
	"fmt"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// adversarialTests covers conformance tests 28–32 (adversarial / D-Agents).
func adversarialTests() []testEntry {
	const g = "Adversarial"
	return []testEntry{
		{ID: 28, Group: g, Name: "Reject Grant signature forgery", Run: testSignatureForgery},
		{ID: 29, Group: g, Name: "Reject Grant replay across different recipient principals (when Grant is bound)", Run: testBoundGrantReplayRejected},
		{ID: 30, Group: g, Name: "Reject agent attempting to use a Grant after agent retirement", Run: testRetiredAgentRejected},
		{ID: 31, Group: g, Name: "Reject Bridge call without idempotency key", Run: testBridgeRequiresIdempotency},
		{ID: 32, Group: g, Name: "Reject delegation that omits caveats present in parent", Run: testDelegationDropsCaveatsRejected},
	}
}

// Test 28: a Grant signed with a *different* root key is rejected as grant_invalid.
func testSignatureForgery(c *client) error {
	bad := c.mintInvalidGrant(grant.PrimitiveVault)
	resp, err := c.do("GET", "/fabric/v1/vault/stream/list/anywhere", nil, map[string]string{
		"X-Fabric-Grant": bad,
	})
	if err != nil {
		return err
	}
	if resp.Status != 401 {
		return fmt.Errorf("expected 401, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "grant_invalid" {
		return fmt.Errorf("expected grant_invalid, got %+v", resp.Error)
	}
	return nil
}

// Test 29: a Grant bound to a specific agent (via `agent-id == <id>`) used by
// a different agent is rejected with caveat_unmet. The bearer asserts their
// agent id via X-Fabric-Agent-Id; if it doesn't match the binding, fail.
func testBoundGrantReplayRejected(c *client) error {
	const agentA = "01J0AGENTAGENTAA0000000001"
	const agentB = "01J0AGENTAGENTBB0000000002"
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, []string{"agent-id == " + agentA})
	// Same agent → succeeds.
	resp, err := c.do("GET", "/fabric/v1/vault/streams/list", nil, map[string]string{
		"X-Fabric-Grant":     gr,
		"X-Fabric-Agent-Id":  agentA,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("bound-agent matching: expected 200, got %d: %s", resp.Status, resp.RawBody)
	}
	// Different agent → caveat_unmet.
	resp, err = c.do("GET", "/fabric/v1/vault/streams/list", nil, map[string]string{
		"X-Fabric-Grant":    gr,
		"X-Fabric-Agent-Id": agentB,
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("bound-agent mismatch: expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet, got %+v", resp.Error)
	}
	return nil
}

// Test 30: a Grant bearing a retired agent's id is rejected with principal_retired.
// The test relies on the Hub's storage having a retired principals row for the
// id we assert. To create one, the conformance harness exposes its store via
// the in-process fixture; for black-box runs we use `nakli-hub` directly… but
// for both modes we use the Hub's pair-and-retire flow at /identity/pair/* in
// conformance. Simpler: we use a synthetic retirement helper exposed via the
// fabric-sdk-go (M3 only): the harness exposes a TestRetirePrincipal hook.
//
// In practice the in-process harness pre-populates a retired principal, and
// the conformance script does the same via a one-shot setup. The test here
// just asserts the Hub rejects requests where X-Fabric-Agent-Id names a
// retired principal.
func testRetiredAgentRejected(c *client) error {
	const retiredAgentID = "01J0RETIREDAGENT00000000001"
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, []string{"agent-id == " + retiredAgentID})
	resp, err := c.do("GET", "/fabric/v1/vault/streams/list", nil, map[string]string{
		"X-Fabric-Grant":    gr,
		"X-Fabric-Agent-Id": retiredAgentID,
	})
	if err != nil {
		return err
	}
	if resp.Status != 401 {
		return fmt.Errorf("expected 401, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "principal_retired" {
		return fmt.Errorf("expected principal_retired, got %+v", resp.Error)
	}
	return nil
}

// Test 31: a Bridge call without X-Fabric-Idempotency-Key is rejected.
func testBridgeRequiresIdempotency(c *client) error {
	gr := c.mintGrant(grant.PrimitiveBridge, "*", []string{"call"}, nil)
	resp, err := c.do("POST", "/fabric/v1/bridge/call", map[string]any{
		"adapter":   "test",
		"operation": "transfer",
		"domain":    "example.com",
	}, map[string]string{
		"X-Fabric-Grant": gr,
	})
	if err != nil {
		return err
	}
	if resp.Status != 400 {
		return fmt.Errorf("expected 400, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "bad_request" {
		return fmt.Errorf("expected bad_request, got %+v", resp.Error)
	}
	return nil
}

// Test 32: a delegation that drops a parent caveat is rejected with scope_denied.
func testDelegationDropsCaveatsRejected(c *client) error {
	parentGrant := c.mintGrant(grant.PrimitiveGrant, "*", []string{"mint"}, nil)
	// Mint a parent with a meaningful caveat.
	resp, err := c.do("POST", "/fabric/v1/grant/mint", map[string]any{
		"recipient_principal_id": "01J0CONFORMANCEREC00000003",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read", "write"},
		},
		"caveats": []string{"only-domain in [example.com]"},
	}, map[string]string{
		"X-Fabric-Grant":           parentGrant,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("setup parent mint: %d %s", resp.Status, resp.RawBody)
	}
	var minted struct {
		Macaroon string `json:"macaroon"`
	}
	_ = json.Unmarshal(resp.Data, &minted)
	// Now mint a child that DROPS the only-domain caveat.
	resp, err = c.do("POST", "/fabric/v1/grant/mint", map[string]any{
		"recipient_principal_id": "01J0CONFORMANCEREC00000004",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read"},
		},
		"caveats":                []string{},
		"parent_grant_macaroon":  minted.Macaroon,
	}, map[string]string{
		"X-Fabric-Grant":           parentGrant,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "scope_denied" {
		return fmt.Errorf("expected scope_denied, got %+v", resp.Error)
	}
	return nil
}
