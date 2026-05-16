package conformance

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// grantTests covers conformance tests 6–16 (Grant + caveats + discharge).
func grantTests() []testEntry {
	const g = "Grant"
	return []testEntry{
		{ID: 6, Group: g, Name: "Reject requests without X-Fabric-Grant (except /health and /discover)", Run: testNoGrantRejected},
		{ID: 7, Group: g, Name: "Reject malformed macaroons", Run: testMalformedMacaroon},
		{ID: 8, Group: g, Name: "Reject expired Grants", Run: testExpiredGrant},
		{ID: 9, Group: g, Name: "Reject Grants whose scope doesn't match the operation", Run: testScopeMismatch},
		{ID: 10, Group: g, Name: "Reject Grants with unmet caveats", Run: testCaveatUnmet},
		{ID: 11, Group: g, Name: "Honor rate caveat (test with bursts)", Run: testRateCaveat},
		{ID: 12, Group: g, Name: "Honor max-amount caveat (Bridge calls)", Run: testMaxAmountCaveat},
		{ID: 13, Group: g, Name: "Honor only-domain caveat (Bridge calls)", Run: testOnlyDomainCaveat},
		{ID: 14, Group: g, Name: "Honor requires-human-approval (return 202, pending operation accessible)", Run: testHumanApprovalCaveat},
		{ID: 15, Group: g, Name: "Refuse delegation that would widen scope", Run: testDelegationWideningRefused},
		{ID: 16, Group: g, Name: "Verify third-party discharge for revocation", Run: testThirdPartyDischarge},
	}
}

// Test 6: requests to authenticated endpoints without X-Fabric-Grant are 401.
// /health and /discover must remain unauthenticated.
func testNoGrantRejected(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/vault/stream/list/whatever", nil, nil)
	if err != nil {
		return err
	}
	if resp.Status != 401 {
		return fmt.Errorf("expected 401, got %d", resp.Status)
	}
	if resp.Error == nil || resp.Error.Code != "grant_missing" {
		return fmt.Errorf("expected grant_missing, got %+v", resp.Error)
	}
	// /health must succeed without a Grant.
	resp, err = c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("/health without grant: expected 200, got %d", resp.Status)
	}
	// /discover must succeed without a Grant.
	resp, err = c.do("GET", "/fabric/v1/discover", nil, nil)
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("/discover without grant: expected 200, got %d", resp.Status)
	}
	return nil
}

// Test 7: malformed macaroon → 401 grant_invalid.
func testMalformedMacaroon(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/vault/stream/list/anywhere", nil, map[string]string{
		"X-Fabric-Grant": "not-a-real-macaroon",
	})
	if err != nil {
		return err
	}
	if resp.Status != 401 {
		return fmt.Errorf("expected 401, got %d", resp.Status)
	}
	if resp.Error == nil || resp.Error.Code != "grant_invalid" {
		return fmt.Errorf("expected grant_invalid, got %+v", resp.Error)
	}
	return nil
}

// Test 8: expired Grant → 403 caveat_unmet.
func testExpiredGrant(c *client) error {
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, []string{"time < " + past})
	resp, err := c.do("GET", "/fabric/v1/vault/stream/list/anywhere", nil, map[string]string{
		"X-Fabric-Grant": gr,
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("expected 403, got %d", resp.Status)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet, got %+v", resp.Error)
	}
	return nil
}

// Test 9: scope mismatch → 403 scope_denied.
func testScopeMismatch(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, nil)
	// Use the vault Grant against a history endpoint.
	resp, err := c.do("GET", "/fabric/v1/history/stream/whatever", nil, map[string]string{
		"X-Fabric-Grant": gr,
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

// Test 10: unmet caveat → 403 caveat_unmet. Uses `operation in [read]` against
// a write request.
func testCaveatUnmet(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, []string{"operation in [read]"})
	resp, err := c.do("POST", "/fabric/v1/vault/append", map[string]any{
		"namespace": "list",
		"stream_id": newULID(),
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": b64([]byte("x")),
		},
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("expected 403, got %d", resp.Status)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet, got %+v", resp.Error)
	}
	return nil
}

// Test 11: rate caveat — bursts beyond the bucket are 429 rate_limited.
func testRateCaveat(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "list", []string{"read", "write"}, []string{"rate <= 2 per minute"})
	streamID := newULID()
	post := func() (*fabricResp, error) {
		return c.do("POST", "/fabric/v1/vault/append", map[string]any{
			"namespace": "list",
			"stream_id": streamID,
			"event": map[string]any{
				"kind":               "rate",
				"payload_ciphertext": b64([]byte("x")),
			},
		}, map[string]string{
			"X-Fabric-Grant":           gr,
			"X-Fabric-Idempotency-Key": idemKey(),
		})
	}
	for i := 0; i < 2; i++ {
		resp, err := post()
		if err != nil {
			return err
		}
		if resp.Status != 200 {
			return fmt.Errorf("burst %d: expected 200, got %d: %s", i, resp.Status, resp.RawBody)
		}
	}
	resp, err := post()
	if err != nil {
		return err
	}
	if resp.Status != 429 {
		return fmt.Errorf("third call: expected 429, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "rate_limited" {
		return fmt.Errorf("expected rate_limited, got %+v", resp.Error)
	}
	return nil
}

// Test 12: max-amount caveat on Bridge calls.
func testMaxAmountCaveat(c *client) error {
	gr := c.mintGrant(grant.PrimitiveBridge, "*", []string{"call"}, []string{"max-amount <= 100 USD"})
	body := map[string]any{
		"adapter":   "test",
		"operation": "transfer",
		"domain":    "example.com",
		"amount":    101,
		"currency":  "USD",
	}
	resp, err := c.do("POST", "/fabric/v1/bridge/call", body, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet, got %+v", resp.Error)
	}
	// And: a sub-cap amount passes caveats and reaches the 501 stub.
	body["amount"] = 50
	resp, err = c.do("POST", "/fabric/v1/bridge/call", body, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 501 {
		return fmt.Errorf("sub-cap call should reach M5.5 stub (501); got %d: %s", resp.Status, resp.RawBody)
	}
	return nil
}

// Test 13: only-domain caveat on Bridge calls.
func testOnlyDomainCaveat(c *client) error {
	gr := c.mintGrant(grant.PrimitiveBridge, "*", []string{"call"}, []string{"only-domain in [example.com, allowed.test]"})
	resp, err := c.do("POST", "/fabric/v1/bridge/call", map[string]any{
		"adapter":   "test",
		"operation": "fetch",
		"domain":    "evil.example.com",
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("disallowed domain: expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet, got %+v", resp.Error)
	}
	// allowed.test is in the list → caveat passes; 501 stub.
	resp, err = c.do("POST", "/fabric/v1/bridge/call", map[string]any{
		"adapter":   "test",
		"operation": "fetch",
		"domain":    "allowed.test",
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 501 {
		return fmt.Errorf("allowed domain should pass to 501 stub; got %d: %s", resp.Status, resp.RawBody)
	}
	return nil
}

// Test 14: requires-human-approval returns 202, and the pending op is fetchable.
func testHumanApprovalCaveat(c *client) error {
	gr := c.mintGrant(grant.PrimitiveBridge, "*", []string{"call", "read"}, []string{"requires-human-approval"})
	resp, err := c.do("POST", "/fabric/v1/bridge/call", map[string]any{
		"adapter":   "test",
		"operation": "transfer",
		"domain":    "example.com",
		"amount":    10,
		"currency":  "USD",
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 202 {
		return fmt.Errorf("expected 202, got %d: %s", resp.Status, resp.RawBody)
	}
	// Body should carry pending_id under data.
	var env struct {
		Data struct {
			PendingID string `json:"pending_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.RawBody, &env); err != nil {
		return fmt.Errorf("could not parse 202 body: %v / %s", err, resp.RawBody)
	}
	if env.Data.PendingID == "" {
		return fmt.Errorf("202 response missing pending_id: %s", resp.RawBody)
	}
	// And the pending row is accessible.
	resp, err = c.do("GET", "/fabric/v1/bridge/pending/"+env.Data.PendingID, nil, map[string]string{
		"X-Fabric-Grant": gr,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("pending lookup: expected 200, got %d: %s", resp.Status, resp.RawBody)
	}
	var pend struct {
		PendingID string `json:"pending_id"`
		Status    string `json:"status"`
	}
	_ = json.Unmarshal(resp.Data, &pend)
	if pend.Status != "pending" {
		return fmt.Errorf("pending status: got %q, want pending", pend.Status)
	}
	return nil
}

// Test 15: a child Grant minted via /grant/mint with parent_grant_macaroon
// cannot widen the parent's scope.
func testDelegationWideningRefused(c *client) error {
	parentGrant := c.mintGrant(grant.PrimitiveGrant, "*", []string{"mint"}, nil)
	// First mint a narrow Grant (the "parent" for this delegation).
	resp, err := c.do("POST", "/fabric/v1/grant/mint", map[string]any{
		"recipient_principal_id": "01J0CONFORMANCEREC00000001",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read"},
		},
		"caveats": []string{},
	}, map[string]string{
		"X-Fabric-Grant":           parentGrant,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("setup parent mint failed: %d %s", resp.Status, resp.RawBody)
	}
	var minted struct {
		Macaroon string `json:"macaroon"`
	}
	_ = json.Unmarshal(resp.Data, &minted)
	if minted.Macaroon == "" {
		return fmt.Errorf("setup parent mint missing macaroon: %s", resp.RawBody)
	}
	// Now try to mint a CHILD that widens (adds 'write').
	resp, err = c.do("POST", "/fabric/v1/grant/mint", map[string]any{
		"recipient_principal_id": "01J0CONFORMANCEREC00000002",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read", "write"},
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
		return fmt.Errorf("expected 403 on widening delegation, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "scope_denied" {
		return fmt.Errorf("expected scope_denied, got %+v", resp.Error)
	}
	return nil
}

// Test 16: third-party discharge. Mint a Grant carrying `discharge-from <url>`.
// Requests without the discharge are rejected; with a fresh discharge they
// succeed; after the Grant is revoked, no new discharge can be obtained.
func testThirdPartyDischarge(c *client) error {
	const verifier = "history://revocations"
	gr := c.mintGrant(grant.PrimitiveVault, "list", []string{"read", "write"}, []string{"discharge-from " + verifier})
	streamID := newULID()
	body := map[string]any{
		"namespace": "list",
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "discharge-test",
			"payload_ciphertext": b64([]byte("x")),
		},
	}
	// Without discharge: rejected.
	resp, err := c.do("POST", "/fabric/v1/vault/append", body, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("without discharge: expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "caveat_unmet" {
		return fmt.Errorf("expected caveat_unmet without discharge, got %+v", resp.Error)
	}

	// Mint a discharge — need a grant-discharge capability for that.
	mintGrant := c.mintGrant(grant.PrimitiveGrant, "*", []string{"discharge"}, nil)
	// Parse our gr macaroon to get its grant_id.
	macBytes, _ := base64.StdEncoding.DecodeString(gr)
	parsed, _ := grant.Parse(macBytes)
	resp, err = c.do("POST", "/fabric/v1/grant/discharge", map[string]any{
		"grant_id":     parsed.Identifier.GrantID,
		"verifier_url": verifier,
	}, map[string]string{
		"X-Fabric-Grant": mintGrant,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("discharge mint: expected 200, got %d: %s", resp.Status, resp.RawBody)
	}
	var disch struct {
		Discharge string `json:"discharge"`
	}
	_ = json.Unmarshal(resp.Data, &disch)
	if disch.Discharge == "" {
		return fmt.Errorf("discharge mint response missing discharge")
	}
	// With discharge: succeeds.
	resp, err = c.do("POST", "/fabric/v1/vault/append", body, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
		"X-Fabric-Discharge":       disch.Discharge,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("with discharge: expected 200, got %d: %s", resp.Status, resp.RawBody)
	}
	// Revoke the Grant.
	revokeGrant := c.mintGrant(grant.PrimitiveGrant, "*", []string{"revoke"}, nil)
	resp, err = c.do("POST", "/fabric/v1/grant/revoke", map[string]any{
		"grant_id": parsed.Identifier.GrantID,
		"reason":   "conformance test 16",
	}, map[string]string{
		"X-Fabric-Grant":           revokeGrant,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("revoke: expected 200, got %d: %s", resp.Status, resp.RawBody)
	}
	// Discharge-mint must now refuse.
	resp, err = c.do("POST", "/fabric/v1/grant/discharge", map[string]any{
		"grant_id":     parsed.Identifier.GrantID,
		"verifier_url": verifier,
	}, map[string]string{
		"X-Fabric-Grant": mintGrant,
	})
	if err != nil {
		return err
	}
	if resp.Status != 403 {
		return fmt.Errorf("post-revoke discharge mint: expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "grant_revoked" {
		return fmt.Errorf("expected grant_revoked on post-revoke discharge mint, got %+v", resp.Error)
	}
	return nil
}
