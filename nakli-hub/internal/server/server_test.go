package server_test

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/server"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// hubFixture starts a fully-wired Hub against an httptest server.
type hubFixture struct {
	cfg   *config.Config
	id    *hubid.Identity
	store *storage.Store
	srv   *server.Server
	ts    *httptest.Server
}

func newHubFixture(t *testing.T, opts ...func(*config.Config)) *hubFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Hub.DataDir = dir
	cfg.Storage.FsyncWrites = false
	for _, opt := range opts {
		opt(cfg)
	}

	id, err := hubid.Generate(func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
	if err != nil {
		t.Fatalf("hubid.Generate: %v", err)
	}
	if err := id.Save(filepath.Join(dir, cfg.Hub.Identity.KeypairFile)); err != nil {
		t.Fatalf("Save identity: %v", err)
	}

	store, err := storage.Open(cfg.SQLitePath(), cfg.BlobsPath())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := server.New(cfg, store, id, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &hubFixture{cfg: cfg, id: id, store: store, srv: srv, ts: ts}
}

// mintGrant produces a Grant signed with the Hub's macaroon root key. The
// identifier carries vault scope with a wildcard namespace, sufficient for
// the M2 gate. Operations are ["read", "write"].
func (h *hubFixture) mintGrant(t *testing.T) string {
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
				Primitive:  grant.PrimitiveVault,
				Namespace:  "*",
				Operations: []string{"read", "write"},
			},
		},
		Caveats: []string{
			"operation in [read, write]",
		},
	})
	if err != nil {
		t.Fatalf("grant.Mint: %v", err)
	}
	return base64.StdEncoding.EncodeToString(g.Macaroon)
}

type successEnv struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

type errorEnv struct {
	OK    bool `json:"ok"`
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// do performs the request and returns (status, body bytes).
func (h *hubFixture) do(t *testing.T, method, path string, body interface{}, headers map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, respBody
}

// doRaw is like do but takes a raw io.Reader body (no JSON marshaling, no
// auto Content-Type). Used by the crate-bucket tests where the proxy needs
// to forward arbitrary bytes through PUT.
func (h *hubFixture) doRaw(t *testing.T, method, path string, body io.Reader, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.ts.URL+path, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, respBody
}

// jsonUnmarshalInternal exposes encoding/json.Unmarshal to neighbour test
// files in this package that don't import encoding/json directly.
func jsonUnmarshalInternal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

func TestHealthEndpoint(t *testing.T) {
	h := newHubFixture(t)
	status, body := h.do(t, "GET", "/fabric/v1/health", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if !env.OK {
		t.Fatalf("ok=false; body=%s", body)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["transport_id"] != h.srv.HubID() {
		t.Errorf("transport_id: got %v, want %s", data["transport_id"], h.srv.HubID())
	}
	if data["version"] != "naklimesh/1.0" {
		t.Errorf("version: got %v", data["version"])
	}
}

func TestDiscoverEndpoint(t *testing.T) {
	h := newHubFixture(t)
	status, body := h.do(t, "GET", "/fabric/v1/discover", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	_ = json.Unmarshal(env.Data, &data)
	if data["transport_type"] != "hub" {
		t.Errorf("transport_type: got %v", data["transport_type"])
	}
}

func TestM2Gate_VaultAppendAndRead(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrant(t)

	streamID, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	idemKey, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	payload := base64.StdEncoding.EncodeToString([]byte("opaque ciphertext for the test"))
	appendBody := map[string]any{
		"namespace": "list",
		"stream_id": streamID.String(),
		"event": map[string]any{
			"kind":               "test-event",
			"payload_ciphertext": payload,
			"vector_clock":       map[string]int64{"device-test": 1},
		},
	}
	headers := map[string]string{
		"X-Fabric-Grant":            g,
		"X-Fabric-Idempotency-Key":  idemKey.String(),
	}
	status, body := h.do(t, "POST", "/fabric/v1/vault/append", appendBody, headers)
	if status != http.StatusOK {
		t.Fatalf("append status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var appendOut struct {
		EventID        string `json:"event_id"`
		SequenceNumber int64  `json:"sequence_number"`
	}
	if err := json.Unmarshal(env.Data, &appendOut); err != nil {
		t.Fatalf("unmarshal append data: %v; body=%s", err, body)
	}
	if appendOut.EventID == "" {
		t.Fatal("event_id missing")
	}
	if appendOut.SequenceNumber != 1 {
		t.Errorf("sequence_number: got %d, want 1", appendOut.SequenceNumber)
	}

	// Read it back.
	readPath := "/fabric/v1/vault/stream/list/" + streamID.String()
	status, body = h.do(t, "GET", readPath, nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("read status: got %d, want 200; body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var readOut struct {
		Events []struct {
			EventID           string `json:"event_id"`
			Kind              string `json:"kind"`
			SequenceNumber    int64  `json:"sequence_number"`
			PayloadCiphertext string `json:"payload_ciphertext"`
		} `json:"events"`
		More bool `json:"more"`
	}
	if err := json.Unmarshal(env.Data, &readOut); err != nil {
		t.Fatalf("unmarshal read data: %v; body=%s", err, body)
	}
	if len(readOut.Events) != 1 {
		t.Fatalf("events length: got %d, want 1", len(readOut.Events))
	}
	if readOut.Events[0].EventID != appendOut.EventID {
		t.Errorf("event_id mismatch: got %s, want %s", readOut.Events[0].EventID, appendOut.EventID)
	}
	if readOut.Events[0].Kind != "test-event" {
		t.Errorf("kind: got %s, want test-event", readOut.Events[0].Kind)
	}
	if readOut.Events[0].PayloadCiphertext != payload {
		t.Errorf("payload mismatch: got %q want %q", readOut.Events[0].PayloadCiphertext, payload)
	}

	// Idempotent replay — same key, same body → same response, original event_id.
	status, body = h.do(t, "POST", "/fabric/v1/vault/append", appendBody, headers)
	if status != http.StatusOK {
		t.Fatalf("replay status: got %d, want 200; body=%s", status, body)
	}
	var replayEnv successEnv
	_ = json.Unmarshal(body, &replayEnv)
	var replayOut struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(replayEnv.Data, &replayOut)
	if replayOut.EventID != appendOut.EventID {
		t.Errorf("replay event_id: got %s, want %s (replay should return original response)", replayOut.EventID, appendOut.EventID)
	}

	// Verify only one event exists in the stream (replay must not duplicate).
	status, body = h.do(t, "GET", readPath, nil, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("read-after-replay status: got %d", status)
	}
	_ = json.Unmarshal(body, &env)
	_ = json.Unmarshal(env.Data, &readOut)
	if len(readOut.Events) != 1 {
		t.Errorf("after replay, events length: got %d, want 1", len(readOut.Events))
	}

	// Same idempotency key + different payload → 409.
	differentBody := map[string]any{
		"namespace": "list",
		"stream_id": streamID.String(),
		"event": map[string]any{
			"kind":               "test-event",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("different ciphertext")),
		},
	}
	status, body = h.do(t, "POST", "/fabric/v1/vault/append", differentBody, headers)
	if status != http.StatusConflict {
		t.Fatalf("idempotency conflict status: got %d, want 409; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "idempotency_conflict" {
		t.Errorf("error.code: got %q, want idempotency_conflict", ee.Error.Code)
	}

	// New idempotency key, same kind → new event with sequence_number=2.
	newKey, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	status, body = h.do(t, "POST", "/fabric/v1/vault/append", appendBody, map[string]string{
		"X-Fabric-Grant":            g,
		"X-Fabric-Idempotency-Key":  newKey.String(),
	})
	if status != http.StatusOK {
		t.Fatalf("second append status: got %d, want 200; body=%s", status, body)
	}
	_ = json.Unmarshal(body, &env)
	var secondOut struct {
		EventID        string `json:"event_id"`
		SequenceNumber int64  `json:"sequence_number"`
	}
	_ = json.Unmarshal(env.Data, &secondOut)
	if secondOut.SequenceNumber != 2 {
		t.Errorf("second sequence_number: got %d, want 2", secondOut.SequenceNumber)
	}
	if secondOut.EventID == appendOut.EventID {
		t.Error("second append should have produced a new event_id")
	}
}

func TestVaultRequiresGrant(t *testing.T) {
	h := newHubFixture(t)
	streamID, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	status, body := h.do(t, "GET", "/fabric/v1/vault/stream/list/"+streamID.String(), nil, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "grant_missing" {
		t.Errorf("error.code: got %q, want grant_missing", ee.Error.Code)
	}
}

func TestVaultRefusesFabricNamespace(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintGrant(t)
	idemKey, _ := ulid.New(ulid.Now(), cryptorand.Reader)
	body := map[string]any{
		"namespace": "fabric.detections",
		"stream_id": "anything",
		"event": map[string]any{
			"kind":               "anomaly",
			"payload_ciphertext": base64.StdEncoding.EncodeToString([]byte("x")),
		},
	}
	status, respBody := h.do(t, "POST", "/fabric/v1/vault/append", body, map[string]string{
		"X-Fabric-Grant":            g,
		"X-Fabric-Idempotency-Key":  idemKey.String(),
	})
	if status != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", status, respBody)
	}
	var ee errorEnv
	_ = json.Unmarshal(respBody, &ee)
	if ee.Error.Code != "scope_denied" {
		t.Errorf("error.code: got %q, want scope_denied", ee.Error.Code)
	}
}

func TestClusterEndpointsReserved(t *testing.T) {
	h := newHubFixture(t)
	status, body := h.do(t, "GET", "/fabric/v1/cluster/anything", nil, nil)
	if status != http.StatusNotImplemented {
		t.Fatalf("status: got %d, want 501; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "not_implemented" {
		t.Errorf("error.code: got %q, want not_implemented", ee.Error.Code)
	}
}

func TestForeignGrantRejected(t *testing.T) {
	h := newHubFixture(t)
	// Mint a Grant with a DIFFERENT root key — should be rejected.
	pub, _, _ := ed25519.GenerateKey(cryptorand.Reader)
	foreignKey := make([]byte, 32)
	_, _ = cryptorand.Read(foreignKey)
	now := time.Now().UTC()
	gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	pid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	g, _ := grant.Mint(grant.MintSpec{
		RootKey: foreignKey,
		Location: h.ts.URL,
		Identifier: grant.Identifier{
			GrantID:           gid.String(),
			IssuedAt:          now,
			IssuedByPrincipal: pid.String(),
			IssuedByKeypair:   pub,
			Scope: grant.Scope{Primitive: grant.PrimitiveVault, Namespace: "*", Operations: []string{"read"}},
		},
	})
	header := base64.StdEncoding.EncodeToString(g.Macaroon)
	status, body := h.do(t, "GET", "/fabric/v1/vault/stream/list/whatever", nil, map[string]string{"X-Fabric-Grant": header})
	if status != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "grant_invalid" {
		t.Errorf("error.code: got %q, want grant_invalid", ee.Error.Code)
	}
}
