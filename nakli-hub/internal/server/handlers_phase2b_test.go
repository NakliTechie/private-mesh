package server_test

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// mintGrantWithScope is like hubFixture.mintGrant but lets a test customize
// scope + caveats. Returns the base64-encoded macaroon.
func (h *hubFixture) mintGrantWithScope(t *testing.T, primitive grant.Primitive, namespace string, operations []string, caveats []string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	pid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	g, err := grant.Mint(grant.MintSpec{
		RootKey:  h.id.MacaroonRootKey,
		Location: h.ts.URL,
		Identifier: grant.Identifier{
			GrantID:           gid.String(),
			IssuedAt:          now,
			IssuedByPrincipal: pid.String(),
			IssuedByKeypair:   pub,
			Scope: grant.Scope{
				Primitive:  primitive,
				Namespace:  namespace,
				Operations: operations,
			},
		},
		Caveats: caveats,
	})
	if err != nil {
		t.Fatalf("mintGrantWithScope: %v", err)
	}
	return base64.StdEncoding.EncodeToString(g.Macaroon)
}

// --- History ---

func TestHistoryAppendVerifyAndChainConflict(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, grant.PrimitiveHistory, "*", []string{"read", "write"}, nil)
	streamID := newULIDStr()

	// First append: previous_event_hash must be empty.
	firstReq := map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "init",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("first")),
			"previous_event_hash": "",
		},
	}
	status, body := h.do(t, "POST", "/fabric/v1/history/append", firstReq, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusOK {
		t.Fatalf("first append: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var firstResp struct {
		EventID   string `json:"event_id"`
		EventHash string `json:"event_hash"`
	}
	_ = json.Unmarshal(env.Data, &firstResp)
	if firstResp.EventID == "" || firstResp.EventHash == "" {
		t.Fatal("first event missing fields")
	}

	// Second append with the correct previous_event_hash.
	secondReq := map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "step",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("second")),
			"previous_event_hash": firstResp.EventHash,
		},
	}
	status, body = h.do(t, "POST", "/fabric/v1/history/append", secondReq, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusOK {
		t.Fatalf("second append: got %d, want 200; body=%s", status, body)
	}

	// Bad previous hash → 409.
	badReq := map[string]any{
		"stream_id": streamID,
		"event": map[string]any{
			"kind":                "bad",
			"payload_ciphertext":  base64.StdEncoding.EncodeToString([]byte("bad")),
			"previous_event_hash": base64.StdEncoding.EncodeToString([]byte("not the right hash")),
		},
	}
	status, body = h.do(t, "POST", "/fabric/v1/history/append", badReq, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusConflict {
		t.Fatalf("bad-hash append: got %d, want 409; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "conflict" {
		t.Errorf("error.code: got %q, want conflict", ee.Error.Code)
	}

	// Read the chain.
	status, body = h.do(t, "GET", "/fabric/v1/history/stream/"+streamID, nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("read: got %d, want 200; body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var readResp struct {
		Events []struct {
			EventID           string `json:"event_id"`
			SequenceNumber    int64  `json:"sequence_number"`
			PreviousEventHash string `json:"previous_event_hash"`
			EventHash         string `json:"event_hash"`
		} `json:"events"`
	}
	_ = json.Unmarshal(env.Data, &readResp)
	if len(readResp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(readResp.Events))
	}
	if readResp.Events[0].PreviousEventHash != "" {
		t.Error("first event should have empty previous_event_hash")
	}
	if readResp.Events[1].PreviousEventHash != firstResp.EventHash {
		t.Error("second event's previous_event_hash should equal first event's event_hash")
	}

	// Verify endpoint.
	status, body = h.do(t, "GET", "/fabric/v1/history/verify/"+streamID, nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("verify: got %d, want 200; body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var verifyResp struct {
		Verified bool  `json:"verified"`
		Length   int64 `json:"length"`
	}
	_ = json.Unmarshal(env.Data, &verifyResp)
	if !verifyResp.Verified {
		t.Errorf("verify should return true")
	}
	if verifyResp.Length != 2 {
		t.Errorf("length: got %d, want 2", verifyResp.Length)
	}
}

// --- Identity ---

func TestIdentityPrincipalEndpoint(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "identity", "*", []string{"read"}, nil)
	status, body := h.do(t, "GET", "/fabric/v1/identity/principal", nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var resp struct {
		PrincipalID string `json:"principal_id"`
	}
	_ = json.Unmarshal(env.Data, &resp)
	if resp.PrincipalID == "" {
		t.Error("principal_id missing")
	}
}

func TestIdentityPairFlow(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "identity", "*", []string{"pair"}, nil)

	// Initiate.
	status, body := h.do(t, "POST", "/fabric/v1/identity/pair/initiate", map[string]any{
		"pairing_method":     "qr",
		"expires_in_seconds": 600,
	}, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("initiate status: got %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var initResp struct {
		PairingToken string `json:"pairing_token"`
		NumericCode  string `json:"numeric_code"`
		QRPayload    string `json:"qr_payload"`
		MagicLink    string `json:"magic_link"`
	}
	_ = json.Unmarshal(env.Data, &initResp)
	if initResp.PairingToken == "" || initResp.NumericCode == "" {
		t.Fatal("missing token or numeric_code")
	}
	if len(initResp.NumericCode) != 6 {
		t.Errorf("numeric_code length: got %d, want 6", len(initResp.NumericCode))
	}

	// Complete — generate a new device keypair on the requesting side.
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	status, body = h.do(t, "POST", "/fabric/v1/identity/pair/complete", map[string]any{
		"pairing_token":         initResp.PairingToken,
		"new_device_public_key": base64.StdEncoding.EncodeToString(pub),
		"new_device_name":       "Test Device",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("complete status: got %d, body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var completeResp struct {
		DeviceID         string `json:"device_id"`
		EnrollmentGrant  string `json:"enrollment_grant"`
		TransportConfigs []map[string]any `json:"transport_configs"`
	}
	_ = json.Unmarshal(env.Data, &completeResp)
	if completeResp.DeviceID == "" {
		t.Error("device_id missing")
	}
	if completeResp.EnrollmentGrant == "" {
		t.Error("enrollment_grant missing")
	}
	if len(completeResp.TransportConfigs) == 0 {
		t.Error("transport_configs empty")
	}
	// Verify the enrollment grant is signed by the Hub.
	enrollBytes, err := base64.StdEncoding.DecodeString(completeResp.EnrollmentGrant)
	if err != nil {
		t.Fatal(err)
	}
	if err := grant.VerifySignature(enrollBytes, h.id.MacaroonRootKey, grant.AlwaysSatisfied); err != nil {
		t.Errorf("enrollment grant signature: %v", err)
	}

	// Re-completing the same token must fail.
	status, body = h.do(t, "POST", "/fabric/v1/identity/pair/complete", map[string]any{
		"pairing_token":         initResp.PairingToken,
		"new_device_public_key": base64.StdEncoding.EncodeToString(pub),
		"new_device_name":       "Other Device",
	}, nil)
	if status != http.StatusConflict {
		t.Errorf("re-complete status: got %d, want 409", status)
	}
}

// --- Grant ---

func TestGrantMintVerifyRevoke(t *testing.T) {
	h := newHubFixture(t)
	parent := h.mintGrantWithScope(t, "grant", "*", []string{"mint", "verify", "revoke"}, nil)

	// Mint a child Grant via the API.
	status, body := h.do(t, "POST", "/fabric/v1/grant/mint", map[string]any{
		"recipient_principal_id": "01JFXAMPLERECIPIENTM10001",
		"scope": map[string]any{
			"primitive":  "vault",
			"namespace":  "list",
			"operations": []string{"read", "write"},
		},
		"caveats": []string{"operation in [read, write]"},
	}, map[string]string{
		"X-Fabric-Grant":           parent,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusOK {
		t.Fatalf("mint status: got %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var mintResp struct {
		GrantID  string `json:"grant_id"`
		Macaroon string `json:"macaroon"`
	}
	_ = json.Unmarshal(env.Data, &mintResp)
	if mintResp.GrantID == "" || mintResp.Macaroon == "" {
		t.Fatal("mint response missing fields")
	}
	// Verify the issued macaroon.
	macBytes, _ := base64.StdEncoding.DecodeString(mintResp.Macaroon)
	if err := grant.VerifySignature(macBytes, h.id.MacaroonRootKey, grant.AlwaysSatisfied); err != nil {
		t.Errorf("issued macaroon: %v", err)
	}

	// /grant/verify should report would_succeed=true for an in-scope op.
	status, body = h.do(t, "POST", "/fabric/v1/grant/verify", map[string]any{
		"macaroon": mintResp.Macaroon,
		"hypothetical_operation": map[string]any{
			"primitive": "vault",
			"namespace": "list",
			"operation": "read",
		},
	}, map[string]string{"X-Fabric-Grant": parent})
	if status != http.StatusOK {
		t.Fatalf("verify status: %d, body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var verifyResp struct {
		WouldSucceed bool     `json:"would_succeed"`
		Reasons      []string `json:"reasons"`
	}
	_ = json.Unmarshal(env.Data, &verifyResp)
	if !verifyResp.WouldSucceed {
		t.Errorf("verify: expected would_succeed=true, got false; reasons=%v", verifyResp.Reasons)
	}

	// /grant/verify with an out-of-scope op should return would_succeed=false.
	status, body = h.do(t, "POST", "/fabric/v1/grant/verify", map[string]any{
		"macaroon": mintResp.Macaroon,
		"hypothetical_operation": map[string]any{
			"primitive": "bridge",
			"namespace": "list",
			"operation": "call",
		},
	}, map[string]string{"X-Fabric-Grant": parent})
	if status != http.StatusOK {
		t.Fatalf("verify-mismatch status: %d", status)
	}
	_ = json.Unmarshal(body, &env)
	_ = json.Unmarshal(env.Data, &verifyResp)
	if verifyResp.WouldSucceed {
		t.Errorf("verify-mismatch: expected would_succeed=false")
	}

	// /grant/revoke writes a revocation event.
	status, body = h.do(t, "POST", "/fabric/v1/grant/revoke", map[string]any{
		"grant_id": mintResp.GrantID,
		"reason":   "test",
	}, map[string]string{
		"X-Fabric-Grant":           parent,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusOK {
		t.Fatalf("revoke status: %d, body=%s", status, body)
	}
}

// --- Stubs: LLM, Bridge, Sync ---

func TestLLMRoutesEmpty(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "llm", "*", []string{"read"}, nil)
	status, body := h.do(t, "GET", "/fabric/v1/llm/routes", nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("status: %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var resp struct {
		Routes []any `json:"routes"`
	}
	_ = json.Unmarshal(env.Data, &resp)
	if len(resp.Routes) != 0 {
		t.Errorf("phase 2b llm routes should be empty; got %d", len(resp.Routes))
	}
}

func TestLLMCompleteNotImplemented(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "llm", "*", []string{"invoke"}, nil)
	status, _ := h.do(t, "POST", "/fabric/v1/llm/complete", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusNotImplemented {
		t.Errorf("status: %d, want 501", status)
	}
}

func TestBridgeAdaptersEmpty(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "bridge", "*", []string{"read"}, nil)
	status, body := h.do(t, "GET", "/fabric/v1/bridge/adapters", nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("status: %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var resp struct {
		Adapters []any `json:"adapters"`
	}
	_ = json.Unmarshal(env.Data, &resp)
	if len(resp.Adapters) != 0 {
		t.Errorf("with no registry installed, bridge/adapters should be empty; got %d", len(resp.Adapters))
	}
}

func TestBridgeAdaptersCatalogue(t *testing.T) {
	h := newHubFixture(t)
	reg := bridge.NewRegistry(nil)
	reg.MustRegister(bridge.NoopAdapter{})
	h.srv.SetBridgeRegistry(reg)
	g := h.mintGrantWithScope(t, "bridge", "*", []string{"read"}, nil)
	status, body := h.do(t, "GET", "/fabric/v1/bridge/adapters", nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("status: %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var resp struct {
		Adapters []struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			Operations []struct {
				Name string `json:"name"`
			} `json:"operations"`
			Status string `json:"status"`
		} `json:"adapters"`
	}
	_ = json.Unmarshal(env.Data, &resp)
	if len(resp.Adapters) != 1 {
		t.Fatalf("expected 1 adapter in catalogue, got %d", len(resp.Adapters))
	}
	if resp.Adapters[0].Name != bridge.NoopAdapterName {
		t.Errorf("name: got %q want %q", resp.Adapters[0].Name, bridge.NoopAdapterName)
	}
	if resp.Adapters[0].Status != "active" {
		t.Errorf("status: got %q want active", resp.Adapters[0].Status)
	}
	if len(resp.Adapters[0].Operations) == 0 {
		t.Error("operations list should not be empty")
	}
}

func TestSyncEndpointsStubbed(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, "sync", "*", []string{"read", "pull", "push", "write"}, nil)
	if status, _ := h.do(t, "GET", "/fabric/v1/sync/peers", nil, map[string]string{"X-Fabric-Grant": g}); status != http.StatusOK {
		t.Errorf("sync/peers status: %d, want 200", status)
	}
	// M7: sync/pull is now live (200 with an empty event set on a fresh Hub).
	if status, _ := h.do(t, "GET", "/fabric/v1/sync/pull?since=0", nil, map[string]string{"X-Fabric-Grant": g}); status != http.StatusOK {
		t.Errorf("sync/pull status: %d, want 200", status)
	}
	// sync/conflict-ack stays 501 until M7.x.
	if status, _ := h.do(t, "POST", "/fabric/v1/sync/conflict-ack", map[string]any{}, map[string]string{"X-Fabric-Grant": g}); status != http.StatusNotImplemented {
		t.Errorf("sync/conflict-ack status: %d, want 501", status)
	}
}

// --- Caveat enforcement ---

func TestExpiredTimeCaveatRejects(t *testing.T) {
	h := newHubFixture(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	g := h.mintGrantWithScope(t, grant.PrimitiveVault, "*", []string{"read", "write"}, []string{"time < " + past})
	streamID := newULIDStr()
	status, body := h.do(t, "POST", "/fabric/v1/vault/append", map[string]any{
		"namespace": "list",
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("x")),
		},
	}, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusForbidden {
		t.Fatalf("status: %d, want 403; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "caveat_unmet" {
		t.Errorf("error.code: got %q, want caveat_unmet", ee.Error.Code)
	}
}

func TestOperationCaveatRejectsWriteWhenOnlyRead(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, grant.PrimitiveVault, "*", []string{"read", "write"}, []string{"operation in [read]"})
	streamID := newULIDStr()
	status, body := h.do(t, "POST", "/fabric/v1/vault/append", map[string]any{
		"namespace": "list",
		"stream_id": streamID,
		"event": map[string]any{
			"kind":               "test",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("x")),
		},
	}, map[string]string{
		"X-Fabric-Grant":           g,
		"X-Fabric-Idempotency-Key": newULIDStr(),
	})
	if status != http.StatusForbidden {
		t.Fatalf("status: %d, want 403; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "caveat_unmet" {
		t.Errorf("error.code: got %q, want caveat_unmet", ee.Error.Code)
	}
}

func TestVaultListStreams(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrantWithScope(t, grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	streamA := newULIDStr()
	streamB := newULIDStr()
	for _, sid := range []string{streamA, streamB} {
		status, _ := h.do(t, "POST", "/fabric/v1/vault/append", map[string]any{
			"namespace": "list",
			"stream_id": sid,
			"event": map[string]any{
				"kind":               "test",
				"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("x")),
			},
		}, map[string]string{
			"X-Fabric-Grant":           g,
			"X-Fabric-Idempotency-Key": newULIDStr(),
		})
		if status != http.StatusOK {
			t.Fatalf("seed append %s: %d", sid, status)
		}
	}
	status, body := h.do(t, "GET", "/fabric/v1/vault/streams/list", nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("list status: %d, body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var resp struct {
		Streams []struct {
			StreamID   string `json:"stream_id"`
			EventCount int64  `json:"event_count"`
		} `json:"streams"`
	}
	_ = json.Unmarshal(env.Data, &resp)
	if len(resp.Streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(resp.Streams))
	}
}

func newULIDStr() string {
	id, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	return id.String()
}
