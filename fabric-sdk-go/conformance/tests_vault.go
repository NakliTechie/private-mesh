package conformance

import (
	"encoding/json"
	"fmt"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// vaultHistoryTests covers conformance tests 21–24.
func vaultHistoryTests() []testEntry {
	const g = "Vault/History"
	return []testEntry{
		{ID: 21, Group: g, Name: "Reject Vault writes to namespace outside Grant scope", Run: testVaultNamespaceScopeRejected},
		{ID: 22, Group: g, Name: "Reject History append with mismatched previous_event_hash → conflict", Run: testHistoryHashMismatchConflict},
		{ID: 23, Group: g, Name: "Return events in causal order on read", Run: testReadCausalOrder},
		{ID: 24, Group: g, Name: "Verify hash chain on /history/verify", Run: testHistoryVerify},
	}
}

// Test 21: Grant scoped to namespace=foo cannot write to namespace=bar.
func testVaultNamespaceScopeRejected(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "foo", []string{"read", "write"}, nil)
	resp, err := c.do("POST", "/fabric/v1/vault/append", map[string]any{
		"namespace": "bar",
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
		return fmt.Errorf("expected 403, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "scope_denied" {
		return fmt.Errorf("expected scope_denied, got %+v", resp.Error)
	}
	return nil
}

// Test 22: History append with the wrong previous_event_hash → 409 conflict.
func testHistoryHashMismatchConflict(c *client) error {
	gr := c.mintGrant(grant.PrimitiveHistory, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	// Seed an initial event.
	first := map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "init",
			"payload_ciphertext":  b64([]byte("first")),
			"previous_event_hash": "",
		},
	}
	resp, err := c.do("POST", "/fabric/v1/history/append", first, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("seed append: got %d %s", resp.Status, resp.RawBody)
	}
	// Now append with a bogus previous_event_hash.
	bad := map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "second",
			"payload_ciphertext":  b64([]byte("second")),
			"previous_event_hash": b64([]byte("not the right hash bytes")),
		},
	}
	resp, err = c.do("POST", "/fabric/v1/history/append", bad, map[string]string{
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
		return fmt.Errorf("expected conflict, got %+v", resp.Error)
	}
	return nil
}

// Test 23: events come back in causal (sequence_number) order.
func testReadCausalOrder(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	for i := 1; i <= 3; i++ {
		resp, err := c.do("POST", "/fabric/v1/vault/append", map[string]any{
			"namespace": "list",
			"stream_id": streamID,
			"event": map[string]any{
				"kind":               fmt.Sprintf("step-%d", i),
				"payload_ciphertext": b64([]byte(fmt.Sprintf("v%d", i))),
			},
		}, map[string]string{
			"X-Fabric-Grant":           gr,
			"X-Fabric-Idempotency-Key": idemKey(),
		})
		if err != nil {
			return err
		}
		if resp.Status != 200 {
			return fmt.Errorf("append %d: %d %s", i, resp.Status, resp.RawBody)
		}
	}
	resp, err := c.do("GET", "/fabric/v1/vault/stream/list/"+streamID, nil, map[string]string{
		"X-Fabric-Grant": gr,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("read: got %d", resp.Status)
	}
	var out struct {
		Events []struct {
			Kind           string `json:"kind"`
			SequenceNumber int64  `json:"sequence_number"`
		} `json:"events"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return err
	}
	if len(out.Events) != 3 {
		return fmt.Errorf("expected 3 events, got %d", len(out.Events))
	}
	for i, ev := range out.Events {
		want := int64(i + 1)
		if ev.SequenceNumber != want {
			return fmt.Errorf("event %d: sequence_number=%d, want %d", i, ev.SequenceNumber, want)
		}
		if ev.Kind != fmt.Sprintf("step-%d", i+1) {
			return fmt.Errorf("event %d: kind=%q, want step-%d", i, ev.Kind, i+1)
		}
	}
	return nil
}

// Test 24: /history/verify reports a verified chain.
func testHistoryVerify(c *client) error {
	gr := c.mintGrant(grant.PrimitiveHistory, "*", []string{"read", "write"}, nil)
	streamID := newULID()
	// Build a two-event chain.
	resp, err := c.do("POST", "/fabric/v1/history/append", map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "a",
			"payload_ciphertext":  b64([]byte("a")),
			"previous_event_hash": "",
		},
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("first append: %d", resp.Status)
	}
	var first struct {
		EventHash string `json:"event_hash"`
	}
	_ = json.Unmarshal(resp.Data, &first)
	resp, err = c.do("POST", "/fabric/v1/history/append", map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "b",
			"payload_ciphertext":  b64([]byte("b")),
			"previous_event_hash": first.EventHash,
		},
	}, map[string]string{
		"X-Fabric-Grant":           gr,
		"X-Fabric-Idempotency-Key": idemKey(),
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("second append: %d %s", resp.Status, resp.RawBody)
	}
	// Verify.
	resp, err = c.do("GET", "/fabric/v1/history/verify/"+streamID, nil, map[string]string{
		"X-Fabric-Grant": gr,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("verify: got %d %s", resp.Status, resp.RawBody)
	}
	var v struct {
		Verified bool  `json:"verified"`
		Length   int64 `json:"length"`
	}
	_ = json.Unmarshal(resp.Data, &v)
	if !v.Verified {
		return fmt.Errorf("verify should be true: %s", resp.RawBody)
	}
	if v.Length != 2 {
		return fmt.Errorf("expected length 2, got %d", v.Length)
	}
	return nil
}
