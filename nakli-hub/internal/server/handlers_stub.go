package server

import (
	"encoding/json"
	"net/http"
)

// Phase 2b stubs for LLM, Bridge, and Sync surfaces. Implementation completes
// in Phase 2c (sync), Phase 2 (full LLM routing), and M5.5 (bridge adapters).
// The shapes here exist so the forward-compat hooks are honored and SDK
// callers see the right errors / catalogues.

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
	// Forward-compat hook 2: the endpoint exists; routing is minimal in v1.0.
	// In Phase 2b the Hub itself advertises no anchor-local or browser-local
	// routes — those are SDK-side or anchor-daemon (nakli-llm-server) in
	// Phase 2. Remote-BYOK routes are caller-configured at SDK level.
	writeSuccess(w, r, http.StatusOK, llmRoutesResp{Routes: []llmRoute{}}, FreshnessNow(s.now()))
}

func (s *Server) handleLLMComplete(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "llm", Operation: "invoke"}); err != nil {
		return
	}
	// v1.0 implementation routes through the SDK's named BYOK provider; the
	// Hub itself doesn't proxy. Return 501 with a clear pointer.
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"Hub does not proxy LLM completions in v1.0; use the SDK's remote-BYOK route directly", false)
}

// --- Bridge ---

type bridgeAdapter struct {
	Name        string   `json:"name"`
	Vendor      string   `json:"vendor"`
	Operations  []string `json:"operations"`
	Status      string   `json:"status"` // "active" | "deprecated" | "experimental"
}

type bridgeAdaptersResp struct {
	Adapters []bridgeAdapter `json:"adapters"`
}

func (s *Server) handleBridgeAdapters(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "read"}); err != nil {
		return
	}
	// Forward-compat hook 3: the discovery endpoint exists from day one so
	// agents can consume it. Phase 2b ships zero adapters — they land at
	// M5.5 with the adapter framework.
	writeSuccess(w, r, http.StatusOK, bridgeAdaptersResp{Adapters: []bridgeAdapter{}}, FreshnessNow(s.now()))
}

func (s *Server) handleBridgeCall(w http.ResponseWriter, r *http.Request) {
	// Body is consumed but ignored; the call is rejected as 501 until M5.5.
	var dummy json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&dummy)
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "call"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"bridge.call is not implemented until M5.5 (adapter framework)", false)
}

func (s *Server) handleBridgeApprove(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "bridge", Operation: "approve"}); err != nil {
		return
	}
	writeError(w, r, http.StatusNotImplemented, ErrNotImplemented,
		"bridge.approve is not implemented until M5.5 (adapter framework)", false)
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
	// v1.0 single-anchor: no peers. The full peer sync ships Phase 2 with
	// multi-anchor support.
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
