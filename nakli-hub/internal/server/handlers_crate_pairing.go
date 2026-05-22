// Unit C — CRATE-PAIR endpoints (Hub-only; cf-worker parity in a follow-up
// milestone). Implements crate-pairing-protocol-v1.0.md §"Phase 1" intent +
// cancel, §"Phase 3" redeem, and §"Capability lifecycle" refresh + revoke.
//
// Auth model note: the spec's "Authorization: Identity {signed-request}"
// wording (Phase 1) is interpreted as "authenticated by user's Identity-bound
// credential" — the Hub's existing macaroon-via-X-Fabric-Grant flow satisfies
// this. /v1/pairing/redeem is unauthenticated; the `secret` IS the auth.
//
// See crate-agent/docs/wire-protocol-audit.md §5 for the gap analysis that
// motivated this work.

package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// expectedTokenType is the literal `type` field value the spec mandates for
// CRATE-PAIR token payloads (crate-pairing-protocol-v1.0.md §"Wire format").
const expectedTokenType = "crate.pairing.token"

// capabilityTTL is the daemon capability's default lifetime — 1 year per spec
// §"Capability lifecycle". Refresh extends by another full year.
const capabilityTTL = 365 * 24 * time.Hour

// --- POST /v1/pairing/intent --------------------------------------------

// cratePairingPayload mirrors the token payload per
// crate-pairing-protocol-v1.0.md §"Wire format". Sent by the browser in the
// intent request body; daemon receives only `secret` + its own fields in
// the redeem request.
type cratePairingPayload struct {
	V                 int    `json:"v"`
	Type              string `json:"type"`
	Secret            string `json:"secret"`
	TransportEndpoint string `json:"transport_endpoint"`
	TransportType     string `json:"transport_type"`
	BucketID          string `json:"bucket_id"`
	IdentityPubkey    string `json:"identity_pubkey"`
	IssuedAt          int64  `json:"issued_at"`  // unix seconds
	ExpiresAt         int64  `json:"expires_at"` // unix seconds
}

// validatePayload returns ("", nil) when valid or (codeConst, message) when
// the payload should be rejected. The protocol spec is strict on `v` and
// `type` to keep forward-compat hooks honest.
func (p *cratePairingPayload) validate(now time.Time) (string, string) {
	if p.V == 0 {
		return ErrBadRequest, "v is required"
	}
	if p.V != 1 {
		return ErrProtocolVersion, "unsupported protocol version"
	}
	if p.Type != expectedTokenType {
		return ErrBadRequest, "type must be \"" + expectedTokenType + "\""
	}
	if p.Secret == "" {
		return ErrBadRequest, "secret is required"
	}
	if p.TransportEndpoint == "" || p.TransportType == "" {
		return ErrBadRequest, "transport_endpoint and transport_type are required"
	}
	if p.BucketID == "" {
		return ErrBadRequest, "bucket_id is required"
	}
	if p.IdentityPubkey == "" {
		return ErrBadRequest, "identity_pubkey is required"
	}
	if p.IssuedAt == 0 || p.ExpiresAt == 0 {
		return ErrBadRequest, "issued_at and expires_at are required"
	}
	if p.ExpiresAt <= p.IssuedAt {
		return ErrBadRequest, "expires_at must be > issued_at"
	}
	if time.Unix(p.ExpiresAt, 0).Before(now) {
		return ErrTokenExpired, "token expires_at is in the past"
	}
	return "", ""
}

func (s *Server) handleCratePairingIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Auth: any macaroon issued under the Hub's root key satisfies the spec's
	// "Identity-bound credential" requirement for browser-side intent issuance.
	// Future versions could narrow this with a `crate-pairing` scope caveat.
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "pair"}); err != nil {
		return
	}

	body, err := decodePayloadBody(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, err.Error(), false)
		return
	}
	var payload cratePairingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}

	now := s.now()
	if code, msg := payload.validate(now); code != "" {
		status := http.StatusBadRequest
		if code == ErrTokenExpired {
			status = http.StatusGone
		} else if code == ErrProtocolVersion {
			status = http.StatusUpgradeRequired
		}
		writeError(w, r, status, code, msg, false)
		return
	}

	// Persist verbatim; payload_json keeps the full row for audit.
	if err := s.store.CreateCratePairingToken(ctx, storage.CratePairingToken{
		Secret:            payload.Secret,
		PayloadJSON:       string(body),
		BucketID:          payload.BucketID,
		IdentityPubkey:    payload.IdentityPubkey,
		TransportEndpoint: payload.TransportEndpoint,
		TransportType:     payload.TransportType,
		IssuedAt:          time.Unix(payload.IssuedAt, 0),
		ExpiresAt:         time.Unix(payload.ExpiresAt, 0),
	}); err != nil {
		// Conflict on existing secret = treat as idempotent success per audit §1.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeSuccess(w, r, http.StatusCreated, struct{}{}, FreshnessNow(s.now()))
			return
		}
		s.logger.Error("CreateCratePairingToken failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not store pairing intent", true)
		return
	}
	writeSuccess(w, r, http.StatusCreated, struct{}{}, FreshnessNow(s.now()))
}

// --- POST /v1/pairing/intent/cancel -------------------------------------

type cratePairingCancelReq struct {
	Secret string `json:"secret"`
}

func (s *Server) handleCratePairingCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "pair"}); err != nil {
		return
	}
	var req cratePairingCancelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.Secret == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "secret is required", false)
		return
	}
	err := s.store.CancelCratePairingToken(ctx, req.Secret)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, ErrTokenNotFound, "no pairing intent matches that secret", false)
			return
		}
		if errors.Is(err, storage.ErrCratePairingTokenAlreadyRedeemed) {
			writeError(w, r, http.StatusConflict, ErrTokenAlreadyRedeemed,
				"token already redeemed; revoke the issued capability via DELETE /v1/capability/{id}", false)
			return
		}
		s.logger.Error("CancelCratePairingToken failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not cancel pairing intent", true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /v1/pairing/redeem (unauthenticated) --------------------------

type cratePairingRedeemReq struct {
	V                 int             `json:"v"`
	Secret            string          `json:"secret"`
	DaemonPubkey      string          `json:"daemon_pubkey"`
	DaemonFingerprint json.RawMessage `json:"daemon_fingerprint"`
}

type cratePairingRedeemResp struct {
	V                int    `json:"v"`
	Capability       string `json:"capability"`        // base64 macaroon
	BucketReference  string `json:"bucket_reference"`
	TransportPubkey  string `json:"transport_pubkey"`  // base64 Ed25519 pubkey
	ExpiresAt        int64  `json:"expires_at"`        // unix seconds
}

func (s *Server) handleCratePairingRedeem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req cratePairingRedeemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.V != 1 {
		writeError(w, r, http.StatusUpgradeRequired, ErrProtocolVersion, "unsupported protocol version", false)
		return
	}
	if req.Secret == "" || req.DaemonPubkey == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "secret and daemon_pubkey are required", false)
		return
	}

	// Look up before redeeming so we can disambiguate not-found / cancelled /
	// already-redeemed / expired into distinct HTTP statuses.
	existing, err := s.store.LookupCratePairingToken(ctx, req.Secret)
	if err != nil {
		writeError(w, r, http.StatusNotFound, ErrTokenNotFound, "token not recognised", false)
		return
	}
	if existing.CancelledAt != nil {
		writeError(w, r, http.StatusNotFound, ErrTokenCancelled, "token was cancelled by the issuer", false)
		return
	}
	if existing.RedeemedAt != nil {
		writeError(w, r, http.StatusConflict, ErrTokenAlreadyRedeemed,
			"token already redeemed; tokens are single-use", false)
		return
	}
	if s.now().After(existing.ExpiresAt) {
		writeError(w, r, http.StatusGone, ErrTokenExpired, "token has expired; generate a new one from the browser", false)
		return
	}

	// Mint the daemon capability — macaroon scoped to sync over the bucket's
	// namespace, with caveats `time < now+1y` and `device-id == daemon_pubkey`.
	now := s.now()
	expires := now.Add(capabilityTTL)
	grantID := newULID()
	id := grant.Identifier{
		GrantID:           grantID,
		IssuedAt:          now,
		IssuedByPrincipal: s.hubID.HubID,
		IssuedByKeypair:   s.hubID.PublicKey,
		Scope: grant.Scope{
			Primitive:  grant.Primitive("sync"),
			Namespace:  existing.BucketID,
			Operations: []string{"read", "write"},
		},
	}
	caveats := []string{
		"time < " + expires.UTC().Format(time.RFC3339Nano),
		"device-id == " + req.DaemonPubkey,
		"operation in [read, write]",
	}
	out, err := grant.Mint(grant.MintSpec{
		RootKey:    s.hubID.MacaroonRootKey,
		Location:   r.Host,
		Identifier: id,
		Caveats:    caveats,
	})
	if err != nil {
		s.logger.Error("crate-pair: capability mint failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not mint capability", true)
		return
	}

	// Atomic redemption — RedeemCratePairingToken's WHERE clause guards
	// against a concurrent redeem winning a race.
	if _, err := s.store.RedeemCratePairingToken(
		ctx, req.Secret, req.DaemonPubkey, string(req.DaemonFingerprint), grantID,
	); err != nil {
		if errors.Is(err, storage.ErrCratePairingTokenAlreadyRedeemed) {
			writeError(w, r, http.StatusConflict, ErrTokenAlreadyRedeemed,
				"token already redeemed by a concurrent caller", false)
			return
		}
		if errors.Is(err, storage.ErrCratePairingTokenExpired) {
			writeError(w, r, http.StatusGone, ErrTokenExpired, "token expired during redemption", false)
			return
		}
		s.logger.Error("RedeemCratePairingToken failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not redeem token", true)
		return
	}

	// Audit-record the freshly-minted capability.
	scopeJSON, _ := json.Marshal(id.Scope)
	cavJSON, _ := json.Marshal(caveats)
	_ = s.store.RememberGrant(ctx, storage.KnownGrant{
		GrantID:            grantID,
		IssuedByPrincipal:  s.hubID.HubID,
		RecipientPrincipal: req.DaemonPubkey,
		ScopeJSON:          string(scopeJSON),
		CaveatsJSON:        string(cavJSON),
		IssuedAt:           now,
		ExpiresAt:          expires,
	})

	writeSuccess(w, r, http.StatusOK, cratePairingRedeemResp{
		V:               1,
		Capability:      base64.StdEncoding.EncodeToString(out.Macaroon),
		BucketReference: existing.BucketID,
		TransportPubkey: base64.StdEncoding.EncodeToString(s.hubID.PublicKey),
		ExpiresAt:       expires.Unix(),
	}, FreshnessNow(s.now()))
}

// --- POST /v1/capability/refresh ----------------------------------------

type capabilityRefreshResp struct {
	V          int    `json:"v"`
	Capability string `json:"capability"` // base64 macaroon
	ExpiresAt  int64  `json:"expires_at"` // unix seconds
}

func (s *Server) handleCapabilityRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Auth: the daemon's CURRENT capability authenticates the refresh request.
	// authMiddleware verifies the macaroon signature + non-revocation already;
	// here we mint a fresh capability with the same scope + caveats.
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "sync", Operation: "read"}); err != nil {
		return
	}
	g := grantFromCtx(ctx)
	if revoked, _ := s.store.IsGrantRevoked(ctx, g.Identifier.GrantID); revoked {
		writeError(w, r, http.StatusUnauthorized, ErrGrantRevoked,
			"capability has been revoked; re-pair to obtain a new one", false)
		return
	}

	now := s.now()
	expires := now.Add(capabilityTTL)
	newGrantID := newULID()

	// Preserve scope + non-time caveats from the current capability; replace
	// `time <` with a fresh expiry.
	newCaveats := make([]string, 0, len(g.Caveats)+1)
	newCaveats = append(newCaveats, "time < "+expires.UTC().Format(time.RFC3339Nano))
	for _, c := range g.Caveats {
		tc := strings.TrimSpace(c)
		if strings.HasPrefix(tc, "time < ") {
			continue
		}
		newCaveats = append(newCaveats, tc)
	}

	id := grant.Identifier{
		GrantID:           newGrantID,
		IssuedAt:          now,
		IssuedByPrincipal: s.hubID.HubID,
		IssuedByKeypair:   s.hubID.PublicKey,
		ParentGrantID:     g.Identifier.GrantID,
		Scope:             g.Identifier.Scope,
	}
	out, err := grant.Mint(grant.MintSpec{
		RootKey:    s.hubID.MacaroonRootKey,
		Location:   r.Host,
		Identifier: id,
		Caveats:    newCaveats,
	})
	if err != nil {
		s.logger.Error("capability refresh mint failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not refresh capability", true)
		return
	}

	scopeJSON, _ := json.Marshal(id.Scope)
	cavJSON, _ := json.Marshal(newCaveats)
	_ = s.store.RememberGrant(ctx, storage.KnownGrant{
		GrantID:            newGrantID,
		IssuedByPrincipal:  s.hubID.HubID,
		RecipientPrincipal: g.Identifier.IssuedByPrincipal, // device-id caveat carries the recipient
		ParentGrantID:      g.Identifier.GrantID,
		ScopeJSON:          string(scopeJSON),
		CaveatsJSON:        string(cavJSON),
		IssuedAt:           now,
		ExpiresAt:          expires,
	})

	writeSuccess(w, r, http.StatusOK, capabilityRefreshResp{
		V:          1,
		Capability: base64.StdEncoding.EncodeToString(out.Macaroon),
		ExpiresAt:  expires.Unix(),
	}, FreshnessNow(s.now()))
}

// --- DELETE /v1/capability/{id} ----------------------------------------

func (s *Server) handleCapabilityRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "capability id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "grant", Operation: "revoke"}); err != nil {
		return
	}
	// Ownership check: the requester must be either the issuer or the
	// recipient of the capability being revoked. Without this, any
	// principal holding any `grant:revoke`-scoped grant could revoke
	// every capability in the Hub — a trivial DoS against legitimate
	// principals. Capabilities minted via the crate-pairing flow are
	// always recorded in grants_known via RememberGrant; capabilities
	// the Hub has never seen fail closed (rejected as not-found).
	if err := s.requireGrantOwnership(w, r, id); err != nil {
		return
	}
	// Reuse the existing grant-revocation plumbing: writes a stub row to
	// grants_known with revoked_at set. authMiddleware checks this on every
	// authenticated request, so the daemon's next call returns 401.
	if err := s.store.MarkGrantRevoked(ctx, id, ""); err != nil {
		s.logger.Error("MarkGrantRevoked (capability) failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not record revocation", true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers -----------------------------------------------------------

// decodePayloadBody reads the full request body and returns it. Used by
// intent so we can persist payload_json verbatim.
func decodePayloadBody(r *http.Request) ([]byte, error) {
	const maxBody = 64 * 1024 // 64 KB is a generous ceiling for a pairing payload
	r.Body = http.MaxBytesReader(nil, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	// Read raw bytes first so we can preserve them verbatim for storage.
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("request body is not valid JSON or exceeds 64 KB")
	}
	return []byte(raw), nil
}
