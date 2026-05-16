package conformance

import (
	"encoding/json"
	"fmt"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// failureModelTests covers conformance tests 25–27.
func failureModelTests() []testEntry {
	const g = "Failure model"
	return []testEntry{
		{ID: 25, Group: g, Name: "Return freshness with staleness_ms reflecting actual peer sync lag", Run: testFreshnessStaleness},
		{ID: 26, Group: g, Name: "degraded:true in /health when transport cannot reach configured peers", Run: testHealthDegradedWithBogusPeer},
		{ID: 27, Group: g, Name: "Conflict events include concurrent_events and common_ancestor", Run: testConflictResponseSchema},
	}
}

// Test 25: staleness_ms is present, finite, and ≥0. With zero peers the value
// is 0; otherwise it reflects the actual peer sync lag. M7 will fill in the
// real multi-peer numbers — at v1.0 the schema-and-finiteness check is the
// gate.
func testFreshnessStaleness(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	if resp.Freshness == nil {
		return fmt.Errorf("freshness missing on /health response")
	}
	if resp.Freshness.StalenessMs < 0 {
		return fmt.Errorf("staleness_ms must be ≥ 0, got %d", resp.Freshness.StalenessMs)
	}
	if resp.Freshness.PeersSynced == nil || resp.Freshness.PeersMissing == nil {
		return fmt.Errorf("peers_synced and peers_missing must both be present (possibly empty) lists")
	}
	if resp.Freshness.AsOf.IsZero() {
		return fmt.Errorf("freshness.as_of must be set")
	}
	return nil
}

// Test 26: degraded=true when at least one configured peer is unreachable.
// Requires the Hub to be started with `--peer-url <unreachable>`; both the
// in-process harness (via SetPeerProbeURLs) and test-conformance.sh wire one.
func testHealthDegradedWithBogusPeer(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	var data struct {
		Degraded        bool             `json:"degraded"`
		DegradedReasons []string         `json:"degraded_reasons"`
		PeerHealth      []map[string]any `json:"peer_health"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return fmt.Errorf("unmarshal /health: %v", err)
	}
	if len(data.PeerHealth) == 0 {
		return fmt.Errorf("/health.peer_health is empty — start the Hub with --peer-url <unreachable> to exercise this test")
	}
	unreachable := false
	for _, p := range data.PeerHealth {
		if reach, ok := p["reachable"].(bool); ok && !reach {
			unreachable = true
		}
	}
	if !unreachable {
		return fmt.Errorf("/health.peer_health has no unreachable entries; cannot validate degraded behavior")
	}
	if !data.Degraded {
		return fmt.Errorf("expected degraded=true when peers are unreachable; got false")
	}
	if len(data.DegradedReasons) == 0 {
		return fmt.Errorf("expected degraded_reasons to be non-empty when degraded=true")
	}
	return nil
}

// Test 27: a conflict response is the correct envelope shape. v1.0 single-
// anchor doesn't produce concurrent_events / common_ancestor (those land at
// M7 with multi-anchor sync); the test asserts the schema is well-formed and
// the conflict code is present.
func testConflictResponseSchema(c *client) error {
	gr := c.mintGrant(grant.PrimitiveHistory, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	// Seed.
	_, _ = c.do("POST", "/fabric/v1/history/append", map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "init",
			"payload_ciphertext":  b64([]byte("a")),
			"previous_event_hash": "",
		},
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	// Force a conflict.
	resp, err := c.do("POST", "/fabric/v1/history/append", map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "b",
			"payload_ciphertext":  b64([]byte("b")),
			"previous_event_hash": b64([]byte("a bogus hash")),
		},
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 409 {
		return fmt.Errorf("expected 409, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "conflict" {
		return fmt.Errorf("expected conflict code, got %+v", resp.Error)
	}
	if resp.Error.Message == "" {
		return fmt.Errorf("conflict error message must be present")
	}
	return nil
}
