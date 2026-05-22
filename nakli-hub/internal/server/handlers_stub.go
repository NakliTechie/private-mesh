package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// Phase 2b/M3/M5.5 surfaces. M3 made the bridge handler enforce caveats
// before the 501 stub so the conformance suite could drive caveat checks.
// M5.5 wires the actual adapter registry: /bridge/adapters surfaces the
// installed catalogue and /bridge/call dispatches via the registry. LLM and
// Sync remain stubs (LLM proxying is intentionally not done by the Hub in
// v1.0; multi-anchor Sync arrives in Phase 2).

// --- LLM ---

type llmRoutesResp struct {
	Routes []llmRoute `json:"routes"`
}

type llmRoute struct {
	Name                  string   `json:"name"`
	Available             bool     `json:"available"`
	LatencyMs             int64    `json:"latency_ms"`
	SupportedCapabilities []string `json:"supported_capabilities"`
}

func (s *Server) handleLLMRoutes(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "llm", Operation: "read"}); err != nil {
		return
	}
	writeSuccess(w, r, http.StatusOK, llmRoutesResp{Routes: []llmRoute{}}, FreshnessNow(s.now()))
}

func (s *Server) handleLLMComplete(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "llm", Operation: "invoke"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"Hub does not proxy LLM completions in v1.0; use the SDK's remote-BYOK route directly", false)
}

// --- Bridge ---

type bridgeAdaptersResp struct {
	Adapters []bridge.CatalogueEntry `json:"adapters"`
}

func (s *Server) handleBridgeAdapters(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "read"}); err != nil {
		return
	}
	catalogue := []bridge.CatalogueEntry{}
	if s.bridge != nil {
		catalogue = s.bridge.Catalogue()
	}
	writeSuccess(w, r, http.StatusOK, bridgeAdaptersResp{Adapters: catalogue}, FreshnessNow(s.now()))
}

type bridgeCallReq struct {
	Adapter     string            `json:"adapter"`
	Operation   string            `json:"operation"`
	Domain      string            `json:"domain"`
	Amount      int64             `json:"amount"`
	Currency    string            `json:"currency"`
	Params      map[string]any    `json:"params"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

type bridgeCallResp struct {
	PendingID string `json:"pending_id"`
	Status    string `json:"status"`
}

// handleBridgeCall: caveat enforcement runs first; on success, dispatch via
// the registered Bridge registry. requires-human-approval short-circuits with
// 202 + pending_id (M3 behavior preserved). When no registry is installed —
// e.g. test fixtures that haven't wired one — the handler returns 501 so
// existing tests against the older shape keep working.
func (s *Server) handleBridgeCall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req bridgeCallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	// Bridge calls implicitly require idempotency keys (spec catalogue).
	if IdempotencyKey(ctx) == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest,
			"X-Fabric-Idempotency-Key is required on Bridge calls", false)
		return
	}
	// SECURITY (P1 #8): derive the effective outbound host from the
	// adapter's params, not the caller-supplied req.Domain. Earlier the
	// caveat `only-domain in [allowed.example.com]` was evaluated against
	// req.Domain — an attacker could send `domain: "allowed.example.com"`
	// alongside `params.url: "http://169.254.169.254/..."` and the Hub
	// would happily perform the request. EffectiveHost is an optional
	// interface implemented by adapters whose target depends on params
	// (webhookpost, openai-compatible today). Adapters that don't
	// implement it fall back to req.Domain (current behavior).
	effectiveDomain := req.Domain
	if s.bridge != nil {
		if a, ok := s.bridge.Get(req.Adapter); ok {
			if eh, ok := a.(bridge.AdapterEffectiveHost); ok {
				derived, herr := eh.EffectiveHost(req.Params)
				if herr != nil {
					writeError(w, r, http.StatusBadRequest, ErrBadRequest, herr.Error(), false)
					return
				}
				if derived != "" {
					if req.Domain != "" && !strings.EqualFold(req.Domain, derived) {
						writeError(w, r, http.StatusBadRequest, ErrBadRequest,
							"request domain does not match the adapter's effective host derived from params", false)
						return
					}
					effectiveDomain = derived
				}
			}
		}
	}
	err := s.checkAuth(w, r, scopeRequirement{
		Primitive:      "bridge",
		Operation:      "call",
		IsBridgeCall:   true,
		BridgeDomain:   effectiveDomain,
		BridgeAmount:   req.Amount,
		BridgeCurrency: req.Currency,
	})
	if err == nil {
		// All caveats satisfied. Dispatch via the registry if one's installed.
		if s.bridge == nil {
			writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
				"bridge.call: no adapter registry installed (this Hub has no adapters)", false)
			return
		}
		params := req.Params
		if params == nil {
			params = map[string]any{}
		}
		resp, callErr := s.bridge.Call(ctx, req.Adapter, &bridge.CallRequest{
			Operation:      req.Operation,
			Params:         params,
			Credentials:    req.Credentials,
			IdempotencyKey: IdempotencyKey(ctx),
		})
		if callErr != nil {
			if errors.Is(callErr, bridge.ErrAdapterNotFound) {
				writeError(w, r, http.StatusNotFound, ErrNotFound, callErr.Error(), false)
				return
			}
			if errors.Is(callErr, bridge.ErrUnknownOperation) {
				writeError(w, r, http.StatusBadRequest, ErrBadRequest, callErr.Error(), false)
				return
			}
			if errors.Is(callErr, bridge.ErrMissingParam) || errors.Is(callErr, bridge.ErrMissingCredential) || errors.Is(callErr, bridge.ErrInvalidParam) {
				writeError(w, r, http.StatusBadRequest, ErrBadRequest, callErr.Error(), false)
				return
			}
			writeError(w, r, http.StatusBadGateway, ErrUnavailable, callErr.Error(), true)
			return
		}
		writeSuccess(w, r, http.StatusOK, map[string]any{
			"adapter":   req.Adapter,
			"operation": req.Operation,
			"result":    resp.Result,
			"metrics":   resp.Metrics,
		}, FreshnessNow(s.now()))
		return
	}
	if !errors.Is(err, errHumanApprovalRequired) {
		// checkAuth already wrote the response.
		return
	}
	// `requires-human-approval` short-circuits with 202 + a pending_id the
	// human can later approve.
	g := grantFromCtx(ctx)
	pendingID := newULID()
	paramsJSON := "{}"
	if req.Params != nil {
		if b, err := json.Marshal(req.Params); err == nil {
			paramsJSON = string(b)
		}
	}
	if err := s.store.InsertPendingBridge(ctx, storage.PendingBridge{
		PendingID:            pendingID,
		GrantID:              g.Identifier.GrantID,
		Adapter:              req.Adapter,
		Operation:            req.Operation,
		ParamsJSON:           paramsJSON,
		RequestedByPrincipal: g.Identifier.IssuedByPrincipal,
	}); err != nil {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not enqueue pending bridge call", true)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	body, _ := json.Marshal(ErrorEnvelope{
		OK: false,
		Error: ErrorBody{
			Code:      ErrHumanApprovalRequired,
			Message:   "this bridge call requires human approval; see pending_id",
			Retryable: false,
		},
	})
	// Append the pending_id to the body so the caller can poll it.
	var env map[string]any
	_ = json.Unmarshal(body, &env)
	env["data"] = bridgeCallResp{PendingID: pendingID, Status: "pending"}
	body, _ = json.Marshal(env)
	_, _ = w.Write(body)
}

// handleBridgeApprove approves a pending row. Real adapter execution lands at
// M5.5; M3 only flips the row's approved_at so a follow-up call can succeed.
func (s *Server) handleBridgeApprove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		PendingID string `json:"pending_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.PendingID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "pending_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "approve"}); err != nil {
		return
	}
	// Approval requires a human principal.
	if t := requesterPrincipalType(r); t != "" && t != "human" {
		writeError(w, r, http.StatusForbidden, ErrCaveatUnmet,
			"bridge.approve requires principal-type=human", false)
		return
	}
	if _, err := s.store.GetPendingBridge(ctx, req.PendingID); err != nil {
		writeError(w, r, http.StatusNotFound, ErrNotFound, "pending bridge id not found", false)
		return
	}
	_, _ = s.store.DB().ExecContext(ctx, `
        UPDATE pending_bridge SET approved_at = ?
        WHERE pending_id = ? AND approved_at IS NULL AND rejected_at IS NULL`,
		s.now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), req.PendingID)
	writeSuccess(w, r, http.StatusOK, map[string]any{"approved": true}, FreshnessNow(s.now()))
}

// handleBridgePending exposes a pending row by id. Used by the conformance
// suite to assert test 14's 202 + pending_id round-trip.
func (s *Server) handleBridgePending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pendingID := r.PathValue("id")
	if pendingID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "pending id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "read"}); err != nil {
		return
	}
	p, err := s.store.GetPendingBridge(ctx, pendingID)
	if err != nil {
		writeError(w, r, http.StatusNotFound, ErrNotFound, "pending bridge id not found", false)
		return
	}
	status := "pending"
	if p.ApprovedAt != nil {
		status = "approved"
	} else if p.RejectedAt != nil {
		status = "rejected"
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"pending_id": p.PendingID,
		"status":     status,
		"adapter":    p.Adapter,
		"operation":  p.Operation,
	}, FreshnessNow(s.now()))
}

// --- Sync ---
//
// M7 wires multi-anchor sync. /sync/peers surfaces locally-discovered mDNS
// peers (via the optional local.Browser the Hub installs at startup);
// /sync/push accepts events from peers and applies them to local storage;
// /sync/pull returns events newer than the caller's `since` cursor.
// /sync/conflict-ack remains 501 until the full conflict-surface lands at
// M7.x (consumers can read the vector clock today; ack is the ack-side).

type syncPeersResp struct {
	Peers []syncPeerOut `json:"peers"`
}

type syncPeerOut struct {
	PeerID       string   `json:"peer_id"`
	TransportID  string   `json:"transport_id"`
	URL          string   `json:"url,omitempty"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities,omitempty"`
	LastSeenAt   string   `json:"last_seen_at,omitempty"`
	FreshnessMs  int64    `json:"freshness_ms"`
}

func (s *Server) handleSyncPeers(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "read"}); err != nil {
		return
	}
	peers := []syncPeerOut{}
	if s.localBrowser != nil {
		for _, p := range s.localBrowser.Peers() {
			peers = append(peers, syncPeerOut{
				PeerID:       p.HubID,
				TransportID:  p.TransportID,
				URL:          p.URL,
				Version:      p.Version,
				Capabilities: p.Capabilities,
				LastSeenAt:   p.LastSeenAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
				FreshnessMs:  int64(s.now().Sub(p.LastSeenAt).Milliseconds()),
			})
		}
	}
	writeSuccess(w, r, http.StatusOK, syncPeersResp{Peers: peers}, FreshnessNow(s.now()))
}

type syncPullResp struct {
	Events []syncEventOut `json:"events"`
	Cursor string         `json:"cursor"`
	More   bool           `json:"more"`
}

type syncEventOut struct {
	Namespace          string           `json:"namespace"`
	StreamID           string           `json:"stream_id"`
	StreamType         string           `json:"stream_type"`
	EventID            string           `json:"event_id"`
	Kind               string           `json:"kind"`
	SequenceNumber     int64            `json:"sequence_number"`
	PayloadCiphertext  string           `json:"payload_ciphertext"`
	PayloadMetadata    json.RawMessage  `json:"payload_metadata,omitempty"`
	CausalDependencies []string         `json:"causal_dependencies"`
	VectorClock        map[string]int64 `json:"vector_clock"`
	PreviousEventHash  string           `json:"previous_event_hash,omitempty"`
	EventHash          string           `json:"event_hash,omitempty"`
	AppendedAt         string           `json:"appended_at"`
	AppendedByPrincipal string          `json:"appended_by_principal"`
}

// handleSyncPull returns events newer than the caller's cursor. Cursor is a
// monotonically-increasing rowid; clients store the cursor and pass it back.
// limit defaults to 100, capped at 1000.
func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "pull"}); err != nil {
		return
	}
	ctx := r.Context()
	since := r.URL.Query().Get("since")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n := atoiSafe(l, 100); n > 0 && n <= 1000 {
			limit = n
		}
	}
	events, nextCursor, more, err := s.store.SyncPull(ctx, since, limit)
	if err != nil {
		s.logger.Error("SyncPull failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "sync.pull failed", true)
		return
	}
	out := syncPullResp{Events: make([]syncEventOut, 0, len(events)), Cursor: nextCursor, More: more}
	for _, ev := range events {
		var vc map[string]int64
		if ev.VectorClock != "" {
			_ = json.Unmarshal([]byte(ev.VectorClock), &vc)
		}
		var deps []string
		if ev.CausalDependencies != "" {
			_ = json.Unmarshal([]byte(ev.CausalDependencies), &deps)
		}
		var meta json.RawMessage
		if ev.PayloadMetadata != "" {
			meta = json.RawMessage(ev.PayloadMetadata)
		}
		out.Events = append(out.Events, syncEventOut{
			Namespace:           ev.Namespace,
			StreamID:            ev.StreamID,
			StreamType:          ev.StreamType,
			EventID:             ev.EventID,
			Kind:                ev.Kind,
			SequenceNumber:      ev.SequenceNumber,
			PayloadCiphertext:   base64Std(ev.PayloadCiphertext),
			PayloadMetadata:     meta,
			CausalDependencies:  deps,
			VectorClock:         vc,
			PreviousEventHash:   base64IfPresent(ev.PreviousEventHash),
			EventHash:           base64IfPresent(ev.EventHash),
			AppendedAt:          ev.AppendedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
			AppendedByPrincipal: ev.AppendedByPrincipal,
		})
	}
	writeSuccess(w, r, http.StatusOK, out, FreshnessNow(s.now()))
}

type syncPushReq struct {
	Events []syncEventIn `json:"events"`
}

type syncEventIn struct {
	Namespace          string           `json:"namespace"`
	StreamID           string           `json:"stream_id"`
	StreamType         string           `json:"stream_type"`
	EventID            string           `json:"event_id"`
	Kind               string           `json:"kind"`
	SequenceNumber     int64            `json:"sequence_number"`
	PayloadCiphertext  string           `json:"payload_ciphertext"`
	PayloadMetadata    json.RawMessage  `json:"payload_metadata,omitempty"`
	CausalDependencies []string         `json:"causal_dependencies"`
	VectorClock        map[string]int64 `json:"vector_clock"`
	PreviousEventHash  string           `json:"previous_event_hash,omitempty"`
	EventHash          string           `json:"event_hash,omitempty"`
	AppendedAt         string           `json:"appended_at"`
	AppendedByPrincipal string          `json:"appended_by_principal"`
	AppendedByGrantID  string           `json:"appended_by_grant_id"`
}

type syncPushResp struct {
	Accepted int      `json:"accepted"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
}

// handleSyncPush accepts events from a peer and applies them to local
// storage if the event_id is not already present. This is multi-master
// sync without conflict resolution beyond idempotency; the receiving
// Hub trusts the sending peer's macaroon Grant + the included
// appended_by_grant_id authorization.
func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "push"}); err != nil {
		return
	}
	ctx := r.Context()
	var req syncPushReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	resp := syncPushResp{}
	for _, ev := range req.Events {
		if ev.EventID == "" || ev.Namespace == "" || ev.StreamID == "" {
			resp.Skipped++
			continue
		}
		ciphertext, err := decodeStdBase64(ev.PayloadCiphertext)
		if err != nil {
			resp.Skipped++
			resp.Errors = append(resp.Errors, "bad ciphertext for "+ev.EventID)
			continue
		}
		blobPath, err := s.store.WriteBlob(ev.Namespace, ev.EventID, ciphertext, s.cfg.Storage.FsyncWrites)
		if err != nil {
			resp.Skipped++
			resp.Errors = append(resp.Errors, "blob write failed for "+ev.EventID+": "+err.Error())
			continue
		}
		in := storage.SyncIngestInput{
			EventID:             ev.EventID,
			Namespace:           ev.Namespace,
			StreamID:            ev.StreamID,
			StreamType:          ev.StreamType,
			Kind:                ev.Kind,
			BlobPath:            blobPath,
			PayloadSize:         int64(len(ciphertext)),
			PayloadMetadata:     string(ev.PayloadMetadata),
			CausalDependencies:  jsonStringArray(ev.CausalDependencies),
			VectorClock:         jsonStringMap(ev.VectorClock),
			PreviousEventHash:   nil,
			EventHash:           nil,
			AppendedByPrincipal: ev.AppendedByPrincipal,
			AppendedByGrantID:   ev.AppendedByGrantID,
		}
		if ev.PreviousEventHash != "" {
			in.PreviousEventHash, _ = decodeStdBase64(ev.PreviousEventHash)
		}
		if ev.EventHash != "" {
			in.EventHash, _ = decodeStdBase64(ev.EventHash)
		}
		if t, err := time.Parse(time.RFC3339Nano, ev.AppendedAt); err == nil {
			in.AppendedAt = t
		}
		if err := s.store.IngestEvent(ctx, in); err != nil {
			if err == storage.ErrAlreadyPresent {
				resp.Skipped++
				continue
			}
			resp.Skipped++
			resp.Errors = append(resp.Errors, "ingest failed for "+ev.EventID+": "+err.Error())
			continue
		}
		resp.Accepted++
	}
	writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
}

func (s *Server) handleSyncConflictAck(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "write"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"sync.conflict_ack ships once conflict surfacing is wired (M7.x)", false)
}

// base64Std re-encodes raw bytes as standard-padded base64. Defined here so
// every handler emits the same encoding regardless of where bytes came from.
func base64Std(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64IfPresent(b)
}

func decodeStdBase64(s string) ([]byte, error) {
	return tryBase64(s)
}
