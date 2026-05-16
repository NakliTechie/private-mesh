package conformance

import (
	"encoding/json"
	"fmt"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// idempotencyTests covers conformance tests 17–20 (idempotency).
func idempotencyTests() []testEntry {
	const g = "Idempotency"
	return []testEntry{
		{ID: 17, Group: g, Name: "Replay same key + same payload → original response", Run: testReplaySameKeySamePayload},
		{ID: 18, Group: g, Name: "Replay same key + different payload → 409 conflict", Run: testReplaySameKeyDifferentPayload},
		{ID: 19, Group: g, Name: "Persist keys ≥ 24 hours", Run: testIdempotencyRetention},
		{ID: 20, Group: g, Name: "Reject state-changing operations missing idempotency key", Run: testRejectMissingIdempotencyKey},
	}
}

// Test 17: same key + same payload → original response (same event_id).
func testReplaySameKeySamePayload(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	key := idemKey()
	body := map[string]any{
		"namespace": "list",
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": b64([]byte("identical payload")),
		},
	}
	headers := map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": key,
	}
	resp1, err := c.do("POST", "/fabric/v1/vault/append", body, headers)
	if err != nil {
		return err
	}
	if resp1.Status != 200 {
		return fmt.Errorf("first call: expected 200, got %d: %s", resp1.Status, resp1.RawBody)
	}
	var first struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(resp1.Data, &first)
	resp2, err := c.do("POST", "/fabric/v1/vault/append", body, headers)
	if err != nil {
		return err
	}
	if resp2.Status != 200 {
		return fmt.Errorf("replay: expected 200, got %d", resp2.Status)
	}
	var second struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(resp2.Data, &second)
	if first.EventID == "" || first.EventID != second.EventID {
		return fmt.Errorf("replay event_id mismatch: %q vs %q", first.EventID, second.EventID)
	}
	return nil
}

// Test 18: same key + different payload → 409.
func testReplaySameKeyDifferentPayload(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	key := idemKey()
	body := map[string]any{
		"namespace": "list",
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": b64([]byte("payload-a")),
		},
	}
	headers := map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": key,
	}
	if r, _ := c.do("POST", "/fabric/v1/vault/append", body, headers); r.Status != 200 {
		return fmt.Errorf("setup append: got %d", r.Status)
	}
	body["event"].(map[string]any)["payload_ciphertext"] = b64([]byte("payload-b"))
	resp, err := c.do("POST", "/fabric/v1/vault/append", body, headers)
	if err != nil {
		return err
	}
	if resp.Status != 409 {
		return fmt.Errorf("expected 409, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "idempotency_conflict" {
		return fmt.Errorf("expected idempotency_conflict, got %+v", resp.Error)
	}
	return nil
}

// Test 19: keys persist ≥ 24 hours. We can't actually fast-forward 24h in a
// conformance run, so we read the configured retention from /discover
// and assert it is ≥ 86400 seconds.
func testIdempotencyRetention(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/discover", nil, nil)
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("/discover: %d", resp.Status)
	}
	var data struct {
		MaxIdempotencyWindowSeconds int64 `json:"max_idempotency_window_seconds"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return fmt.Errorf("unmarshal: %v", err)
	}
	if data.MaxIdempotencyWindowSeconds < 86400 {
		return fmt.Errorf("max_idempotency_window_seconds=%d, want >= 86400", data.MaxIdempotencyWindowSeconds)
	}
	return nil
}

// Test 20: state-changing endpoint with no X-Fabric-Idempotency-Key → 400.
func testRejectMissingIdempotencyKey(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	resp, err := c.do("POST", "/fabric/v1/vault/append", map[string]any{
		"namespace": "list",
		"stream_id": newULID(),
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": b64([]byte("x")),
		},
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
