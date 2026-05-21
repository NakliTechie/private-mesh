package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// routes wires the protocol endpoints to handler funcs. Unauthenticated
// endpoints (/health, /discover, /identity/pair/complete) skip authMiddleware.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /fabric/v1/health", s.handleHealth)
	mux.HandleFunc("GET /fabric/v1/discover", s.handleDiscover)

	// Vault.
	mux.Handle("POST /fabric/v1/vault/append",
		s.authMiddleware(s.idempotencyMiddleware("vault/append", http.HandlerFunc(s.handleVaultAppend))))
	mux.Handle("GET /fabric/v1/vault/stream/{namespace}/{stream_id}",
		s.authMiddleware(http.HandlerFunc(s.handleVaultRead)))
	mux.Handle("GET /fabric/v1/vault/streams/{namespace}",
		s.authMiddleware(http.HandlerFunc(s.handleVaultListStreams)))
	mux.Handle("POST /fabric/v1/vault/subscribe",
		s.authMiddleware(http.HandlerFunc(s.handleVaultSubscribe)))

	// History.
	mux.Handle("POST /fabric/v1/history/append",
		s.authMiddleware(s.idempotencyMiddleware("history/append", http.HandlerFunc(s.handleHistoryAppend))))
	mux.Handle("GET /fabric/v1/history/stream/{stream_id}",
		s.authMiddleware(http.HandlerFunc(s.handleHistoryRead)))
	mux.Handle("GET /fabric/v1/history/verify/{stream_id}",
		s.authMiddleware(http.HandlerFunc(s.handleHistoryVerify)))

	// Identity.
	mux.Handle("GET /fabric/v1/identity/principal",
		s.authMiddleware(http.HandlerFunc(s.handleIdentityPrincipal)))
	mux.Handle("POST /fabric/v1/identity/pair/initiate",
		s.authMiddleware(http.HandlerFunc(s.handlePairInitiate)))
	// pair/complete is unauthenticated — the pairing_token is the auth.
	mux.HandleFunc("POST /fabric/v1/identity/pair/complete", s.handlePairComplete)

	// Grant.
	mux.Handle("POST /fabric/v1/grant/mint",
		s.authMiddleware(s.idempotencyMiddleware("grant/mint", http.HandlerFunc(s.handleGrantMint))))
	mux.Handle("POST /fabric/v1/grant/verify",
		s.authMiddleware(http.HandlerFunc(s.handleGrantVerify)))
	mux.Handle("POST /fabric/v1/grant/revoke",
		s.authMiddleware(s.idempotencyMiddleware("grant/revoke", http.HandlerFunc(s.handleGrantRevoke))))
	mux.Handle("POST /fabric/v1/grant/discharge",
		s.authMiddleware(http.HandlerFunc(s.handleGrantDischarge)))

	// LLM (Phase 2 surface; minimal v1.0 routing).
	mux.Handle("GET /fabric/v1/llm/routes",
		s.authMiddleware(http.HandlerFunc(s.handleLLMRoutes)))
	mux.Handle("POST /fabric/v1/llm/complete",
		s.authMiddleware(s.idempotencyMiddleware("llm/complete", http.HandlerFunc(s.handleLLMComplete))))

	// Bridge (M5.5 fills in the adapter framework).
	mux.Handle("GET /fabric/v1/bridge/adapters",
		s.authMiddleware(http.HandlerFunc(s.handleBridgeAdapters)))
	mux.Handle("POST /fabric/v1/bridge/call",
		s.authMiddleware(s.idempotencyMiddleware("bridge/call", http.HandlerFunc(s.handleBridgeCall))))
	mux.Handle("POST /fabric/v1/bridge/approve",
		s.authMiddleware(http.HandlerFunc(s.handleBridgeApprove)))
	mux.Handle("GET /fabric/v1/bridge/pending/{id}",
		s.authMiddleware(http.HandlerFunc(s.handleBridgePending)))

	// Sync (Phase 2 multi-anchor).
	mux.Handle("GET /fabric/v1/sync/peers",
		s.authMiddleware(http.HandlerFunc(s.handleSyncPeers)))
	mux.Handle("GET /fabric/v1/sync/pull",
		s.authMiddleware(http.HandlerFunc(s.handleSyncPull)))
	mux.Handle("POST /fabric/v1/sync/push",
		s.authMiddleware(http.HandlerFunc(s.handleSyncPush)))
	mux.Handle("POST /fabric/v1/sync/conflict-ack",
		s.authMiddleware(http.HandlerFunc(s.handleSyncConflictAck)))

	// CRATE-PAIR — Unit C. Top-level /v1/* paths (not under /fabric/v1/) per
	// crate-pairing-protocol-v1.0.md. Phase 1 + intent cancel authenticated;
	// Phase 3 redeem unauthenticated (the secret IS the auth).
	mux.Handle("POST /v1/pairing/intent",
		s.authMiddleware(http.HandlerFunc(s.handleCratePairingIntent)))
	mux.Handle("POST /v1/pairing/intent/cancel",
		s.authMiddleware(http.HandlerFunc(s.handleCratePairingCancel)))
	mux.HandleFunc("POST /v1/pairing/redeem", s.handleCratePairingRedeem)
	mux.Handle("POST /v1/capability/refresh",
		s.authMiddleware(http.HandlerFunc(s.handleCapabilityRefresh)))
	mux.Handle("DELETE /v1/capability/{id}",
		s.authMiddleware(http.HandlerFunc(s.handleCapabilityRevoke)))

	// CRATE bucket-proxy — crate-agent M3 piece 1. Register uses identity:pair;
	// metadata + object + list use sync scope on bucket_id. Object verbs share
	// one handler (dispatched by method); LIST is its own route because the
	// query-string translation is non-trivial.
	mux.Handle("POST /v1/crate/bucket/register",
		s.authMiddleware(http.HandlerFunc(s.handleCrateBucketRegister)))
	mux.Handle("GET /v1/crate/bucket",
		s.authMiddleware(http.HandlerFunc(s.handleCrateBucketList)))
	mux.Handle("GET /v1/crate/bucket/{bucket_id}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateBucketMetadata)))
	mux.Handle("HEAD /v1/crate/object/{bucket_id}/{path...}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateObject)))
	mux.Handle("GET /v1/crate/object/{bucket_id}/{path...}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateObject)))
	mux.Handle("PUT /v1/crate/object/{bucket_id}/{path...}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateObject)))
	mux.Handle("DELETE /v1/crate/object/{bucket_id}/{path...}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateObject)))
	mux.Handle("GET /v1/crate/list/{bucket_id}",
		s.authMiddleware(http.HandlerFunc(s.handleCrateList)))

	// Forward-compat hook 4: reserve the cluster/* namespace with 501.
	mux.HandleFunc("/fabric/v1/cluster/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
			"cluster endpoints are not implemented in v1.0 (reserved for v2.0)", false)
	})
}

// --- /health ---

type healthData struct {
	TransportID      string           `json:"transport_id"`
	Version          string           `json:"version"`
	BinaryVersion    string           `json:"binary_version"`
	UptimeSeconds    int64            `json:"uptime_seconds"`
	Degraded         bool             `json:"degraded"`
	DegradedReasons  []string         `json:"degraded_reasons"`
	PeerHealth       []map[string]any `json:"peer_health"`
	QueueDepth       int64            `json:"queue_depth"`
	BlobCount        int64            `json:"blob_count"`
	BlobTotalBytes   int64            `json:"blob_total_bytes"`
	EventCount       int64            `json:"event_count"`
	PrincipalsCount  map[string]int64 `json:"principals_count"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := s.store.PrincipalCounts(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "health: principals count failed", true)
		return
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	var eventCount int64
	_ = s.store.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM events`).Scan(&eventCount)

	reachable, unreachable := s.peerReachability(ctx)
	degraded := len(unreachable) > 0
	reasons := []string{}
	peerHealth := []map[string]any{}
	for _, u := range reachable {
		peerHealth = append(peerHealth, map[string]any{"peer": u, "reachable": true})
	}
	for _, u := range unreachable {
		peerHealth = append(peerHealth, map[string]any{"peer": u, "reachable": false})
		reasons = append(reasons, "peer unreachable: "+u)
	}

	data := healthData{
		TransportID:     s.hubID.HubID,
		Version:         ProtocolVersion,
		BinaryVersion:   s.binVer,
		UptimeSeconds:   int64(s.now().Sub(s.startAt).Seconds()),
		Degraded:        degraded,
		DegradedReasons: reasons,
		PeerHealth:      peerHealth,
		EventCount:      eventCount,
		PrincipalsCount: counts,
	}
	writeSuccess(w, r, http.StatusOK, data, FreshnessNow(s.now()))
}

// --- /discover ---

type discoverData struct {
	TransportType              string   `json:"transport_type"`
	TransportID                string   `json:"transport_id"`
	Version                    string   `json:"version"`
	SupportedPrimitives        []string `json:"supported_primitives"`
	SupportedCaveats           []string `json:"supported_caveats"`
	MaxEventSizeBytes          int64    `json:"max_event_size_bytes"`
	MaxIdempotencyWindowSeconds int64   `json:"max_idempotency_window_seconds"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	data := discoverData{
		TransportType: "hub",
		TransportID:   s.hubID.HubID,
		Version:       ProtocolVersion,
		// Phase 2a wires vault to handlers; the others ship with M2b.
		SupportedPrimitives: []string{
			"vault", "history", "sync", "grant", "identity", "llm", "bridge",
		},
		SupportedCaveats: []string{
			"time", "principal-type", "agent-id", "device-id", "operation",
			"namespace", "rate", "max-amount", "only-domain",
			"requires-human-approval", "nondelegatable", "idempotency-required",
			"discharge-from",
		},
		MaxEventSizeBytes:           s.cfg.Storage.MaxEventSizeBytes,
		MaxIdempotencyWindowSeconds: s.cfg.Idempotency.RetentionSeconds,
	}
	writeSuccess(w, r, http.StatusOK, data, FreshnessNow(s.now()))
}

// --- /vault/append ---

type vaultAppendReq struct {
	Namespace string `json:"namespace"`
	StreamID  string `json:"stream_id"`
	Event     struct {
		Kind               string          `json:"kind"`
		PayloadCiphertext  string          `json:"payload_ciphertext"` // base64
		PayloadMetadata    json.RawMessage `json:"payload_metadata"`
		CausalDependencies []string        `json:"causal_dependencies"`
		VectorClock        map[string]int64 `json:"vector_clock"`
	} `json:"event"`
}

type vaultAppendResp struct {
	EventID        string `json:"event_id"`
	SequenceNumber int64  `json:"sequence_number"`
}

// fabricReservedNamespaces enforces forward-compat hook 6: non-Hub principals
// cannot write to fabric.* namespaces.
var fabricReservedNamespaces = map[string]struct{}{
	"fabric.detections": {},
	"fabric.federation": {},
	"fabric.cluster":    {},
}

func (s *Server) handleVaultAppend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req vaultAppendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.Namespace == "" || req.StreamID == "" || req.Event.Kind == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "namespace, stream_id, and event.kind are required", false)
		return
	}
	if _, reserved := fabricReservedNamespaces[req.Namespace]; reserved {
		// Phase 2a: refuse all writes to fabric.* from any caller. M2b refines
		// this to allow the Hub itself once the Hub has a self-principal.
		writeError(w, r, http.StatusForbidden, ErrScopeDenied,
			"namespace is reserved for the fabric and cannot be written by user principals", false)
		return
	}
	// Auth check before any disk write. Earlier ordering wrote the blob
	// first, leaving an orphan file under blobs/aa/bb/<event_id>.bin every
	// time an unauthorized request was rejected — a disk-exhaustion DoS
	// for any principal holding a scope-mismatched grant.
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "vault",
		Namespace: req.Namespace,
		Operation: "write",
	}); err != nil {
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(req.Event.PayloadCiphertext)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "event.payload_ciphertext is not valid base64", false)
		return
	}
	if int64(len(ciphertext)) > s.cfg.Storage.MaxEventSizeBytes {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "event payload exceeds max_event_size_bytes", false)
		return
	}

	eventID := newULID()
	blobPath, err := s.store.WriteBlob(req.Namespace, eventID, ciphertext, s.cfg.Storage.FsyncWrites)
	if err != nil {
		s.logger.Error("WriteBlob failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "blob persist failed", true)
		return
	}

	g := grantFromCtx(ctx)

	in := storage.AppendInput{
		Namespace:           req.Namespace,
		StreamID:            req.StreamID,
		StreamType:          storage.StreamVault,
		Kind:                req.Event.Kind,
		PayloadCiphertext:   ciphertext,
		PayloadMetadata:     rawOrEmpty(req.Event.PayloadMetadata),
		CausalDependencies:  jsonStringArray(req.Event.CausalDependencies),
		VectorClock:         jsonStringMap(req.Event.VectorClock),
		AppendedByPrincipal: g.Identifier.IssuedByPrincipal,
		AppendedByGrantID:   g.Identifier.GrantID,
	}
	result, err := s.store.AppendEvent(ctx, in, blobPath, eventID)
	if err != nil {
		s.logger.Error("AppendEvent failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "event persist failed", true)
		return
	}
	writeSuccess(w, r, http.StatusOK, vaultAppendResp{
		EventID:        result.EventID,
		SequenceNumber: result.SequenceNumber,
	}, FreshnessNow(s.now()))
}

// --- /vault/stream/{namespace}/{stream_id} ---

type vaultEventOut struct {
	EventID            string           `json:"event_id"`
	Kind               string           `json:"kind"`
	SequenceNumber     int64            `json:"sequence_number"`
	PayloadCiphertext  string           `json:"payload_ciphertext"` // base64
	PayloadMetadata    json.RawMessage  `json:"payload_metadata,omitempty"`
	CausalDependencies []string         `json:"causal_dependencies"`
	VectorClock        map[string]int64 `json:"vector_clock"`
	PreviousEventHash  string           `json:"previous_event_hash,omitempty"` // base64; vault leaves it empty
	EventHash          string           `json:"event_hash,omitempty"`
	AppendedAt         time.Time        `json:"appended_at"`
	AppendedByPrincipal string          `json:"appended_by_principal"`
	AppendedByDevice    string          `json:"appended_by_device,omitempty"`
}

type vaultReadResp struct {
	Events []vaultEventOut `json:"events"`
	More   bool            `json:"more"`
}

func (s *Server) handleVaultRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := r.PathValue("namespace")
	streamID := r.PathValue("stream_id")
	if namespace == "" || streamID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "namespace and stream_id are required", false)
		return
	}
	since := r.URL.Query().Get("since")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		// best-effort parse; cap at 1000
		if n := atoiSafe(l, 100); n > 0 && n <= 1000 {
			limit = n
		}
	}

	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "vault",
		Namespace: namespace,
		Operation: "read",
	}); err != nil {
		return
	}

	rows, more, err := s.store.ReadStream(ctx, namespace, streamID, storage.ReadOptions{
		SinceEventID: since,
		Limit:        limit,
	})
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, r, http.StatusNotFound, ErrNotFound, "since event_id not found in this stream", false)
			return
		}
		s.logger.Error("ReadStream failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "stream read failed", true)
		return
	}
	out := vaultReadResp{Events: make([]vaultEventOut, 0, len(rows)), More: more}
	for _, ev := range rows {
		var meta json.RawMessage
		if ev.PayloadMetadata != "" {
			meta = json.RawMessage(ev.PayloadMetadata)
		}
		var vc map[string]int64
		if ev.VectorClock != "" {
			_ = json.Unmarshal([]byte(ev.VectorClock), &vc)
		}
		var deps []string
		if ev.CausalDependencies != "" {
			_ = json.Unmarshal([]byte(ev.CausalDependencies), &deps)
		}
		out.Events = append(out.Events, vaultEventOut{
			EventID:            ev.EventID,
			Kind:               ev.Kind,
			SequenceNumber:     ev.SequenceNumber,
			PayloadCiphertext:  base64.StdEncoding.EncodeToString(ev.PayloadCiphertext),
			PayloadMetadata:    meta,
			CausalDependencies: deps,
			VectorClock:        vc,
			PreviousEventHash:  base64IfPresent(ev.PreviousEventHash),
			EventHash:          base64IfPresent(ev.EventHash),
			AppendedAt:         ev.AppendedAt,
			AppendedByPrincipal: ev.AppendedByPrincipal,
			AppendedByDevice:    ev.AppendedByDevice,
		})
	}
	writeSuccess(w, r, http.StatusOK, out, FreshnessNow(s.now()))
}

// --- small helpers ---

func newULID() string {
	id, err := ulid.New(ulid.Now(), rand.Reader)
	if err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	return id.String()
}

func rawOrEmpty(rm json.RawMessage) string {
	if len(rm) == 0 {
		return ""
	}
	return string(rm)
}

func jsonStringArray(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func jsonStringMap(m map[string]int64) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func base64IfPresent(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func atoiSafe(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func init() {
	// silence "imported but not used" if path-parsing helpers are removed.
	_ = strings.TrimSpace
}
