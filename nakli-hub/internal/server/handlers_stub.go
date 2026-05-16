package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// Phase 2b/M3 surfaces for LLM, Bridge, and Sync. M3 made the bridge handler
// enforce caveats before the 501 short-circuit so the conformance suite can
// drive max-amount / only-domain / requires-human-approval / idempotency-
// required checks. Real adapter execution lands at M5.5.

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

type bridgeAdapter struct {
	Name        string   `json:"name"`
	Vendor      string   `json:"vendor"`
	Operations  []string `json:"operations"`
	Status      string   `json:"status"`
}

type bridgeAdaptersResp struct {
	Adapters []bridgeAdapter `json:"adapters"`
}

func (s *Server) handleBridgeAdapters(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "read"}); err != nil {
		return
	}
	writeSuccess(w, r, http.StatusOK, bridgeAdaptersResp{Adapters: []bridgeAdapter{}}, FreshnessNow(s.now()))
}

type bridgeCallReq struct {
	Adapter   string          `json:"adapter"`
	Operation string          `json:"operation"`
	Domain    string          `json:"domain"`
	Amount    int64           `json:"amount"`
	Currency  string          `json:"currency"`
	Params    json.RawMessage `json:"params"`
}

type bridgeCallResp struct {
	PendingID string `json:"pending_id"`
	Status    string `json:"status"`
}

// handleBridgeCall: caveat enforcement runs first so the conformance suite can
// drive `max-amount`, `only-domain`, `requires-human-approval`, and the
// implicit `idempotency-required`-on-bridge rule. Only when every check passes
// does the handler return 501 — actual adapter execution arrives at M5.5.
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
	err := s.checkAuth(w, r, scopeRequirement{
		Primitive:      "bridge",
		Operation:      "call",
		IsBridgeCall:   true,
		BridgeDomain:   req.Domain,
		BridgeAmount:   req.Amount,
		BridgeCurrency: req.Currency,
	})
	if err == nil {
		// All caveats satisfied; execution lands at M5.5.
		writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
			"bridge.call execution lands at M5.5 (adapter framework); caveats satisfied", false)
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
	paramsJSON := string(req.Params)
	if paramsJSON == "" {
		paramsJSON = "{}"
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

type syncPeersResp struct {
	Peers []syncPeerOut `json:"peers"`
}

type syncPeerOut struct {
	PeerID       string `json:"peer_id"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
	FreshnessMs  int64  `json:"freshness_ms"`
}

func (s *Server) handleSyncPeers(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "read"}); err != nil {
		return
	}
	writeSuccess(w, r, http.StatusOK, syncPeersResp{Peers: []syncPeerOut{}}, FreshnessNow(s.now()))
}

func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "pull"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"sync.pull is single-anchor-only in v1.0; multi-anchor sync ships in Phase 2", false)
}

func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "push"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"sync.push is single-anchor-only in v1.0; multi-anchor sync ships in Phase 2", false)
}

func (s *Server) handleSyncConflictAck(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "write"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"sync.conflict_ack ships once conflict surfacing is wired (Phase 2)", false)
}
