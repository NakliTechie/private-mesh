package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// --- POST /fabric/v1/grant/mint ---

type grantMintReq struct {
	RecipientPrincipalID string                 `json:"recipient_principal_id"`
	Scope                grantScopeReq          `json:"scope"`
	Caveats              []string               `json:"caveats"`
	ExpiresAt            *time.Time             `json:"expires_at"`
	ParentGrantMacaroon  string                 `json:"parent_grant_macaroon,omitempty"` // base64; for delegation
}

type grantScopeReq struct {
	Primitive  string   `json:"primitive"`
	Namespace  string   `json:"namespace"`
	Operations []string `json:"operations"`
}

type grantMintResp struct {
	GrantID  string `json:"grant_id"`
	Macaroon string `json:"macaroon"` // base64
}

func (s *Server) handleGrantMint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req grantMintReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.RecipientPrincipalID == "" || req.Scope.Primitive == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "recipient_principal_id and scope.primitive are required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive:    "grant",
		Operation:    "mint",
		IsDelegation: req.ParentGrantMacaroon != "",
	}); err != nil {
		return
	}
	g := grantFromCtx(ctx)
	now := s.now()
	expiresAt := now.Add(30 * 24 * time.Hour)
	if req.ExpiresAt != nil {
		expiresAt = *req.ExpiresAt
	}
	grantID := newULID()
	id := grant.Identifier{
		GrantID:           grantID,
		IssuedAt:          now,
		IssuedByPrincipal: g.Identifier.IssuedByPrincipal,
		IssuedByKeypair:   g.Identifier.IssuedByKeypair,
		ParentGrantID:     g.Identifier.GrantID,
		Scope: grant.Scope{
			Primitive:  grant.Primitive(req.Scope.Primitive),
			Namespace:  req.Scope.Namespace,
			Operations: req.Scope.Operations,
		},
	}
	caveats := append([]string{
		"time < " + expiresAt.UTC().Format(time.RFC3339Nano),
	}, req.Caveats...)
	out, err := grant.Mint(grant.MintSpec{
		RootKey:    s.hubID.MacaroonRootKey,
		Location:   r.Host,
		Identifier: id,
		Caveats:    caveats,
	})
	if err != nil {
		s.logger.Error("Mint failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not mint grant", true)
		return
	}
	// Record the Grant in grants_known for audit + future revocation.
	scopeJSON, _ := json.Marshal(req.Scope)
	cavJSON, _ := json.Marshal(caveats)
	_ = s.store.RememberGrant(ctx, storage.KnownGrant{
		GrantID:            grantID,
		IssuedByPrincipal:  g.Identifier.IssuedByPrincipal,
		RecipientPrincipal: stripFabricSuffix(req.RecipientPrincipalID),
		ParentGrantID:      g.Identifier.GrantID,
		ScopeJSON:          string(scopeJSON),
		CaveatsJSON:        string(cavJSON),
		IssuedAt:           now,
		ExpiresAt:          expiresAt,
	})
	writeSuccess(w, r, http.StatusOK, grantMintResp{
		GrantID:  grantID,
		Macaroon: base64.StdEncoding.EncodeToString(out.Macaroon),
	}, FreshnessNow(s.now()))
}

// --- POST /fabric/v1/grant/verify ---

type grantVerifyReq struct {
	Macaroon              string `json:"macaroon"` // base64 (preferred)
	HypotheticalOperation struct {
		Primitive string `json:"primitive"`
		Namespace string `json:"namespace"`
		Operation string `json:"operation"`
	} `json:"hypothetical_operation"`
}

type grantVerifyResp struct {
	WouldSucceed bool     `json:"would_succeed"`
	Reasons      []string `json:"reasons"`
}

func (s *Server) handleGrantVerify(w http.ResponseWriter, r *http.Request) {
	var req grantVerifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "grant", Operation: "verify"}); err != nil {
		return
	}
	if req.Macaroon == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "macaroon is required", false)
		return
	}
	macBytes, err := base64.StdEncoding.DecodeString(req.Macaroon)
	if err != nil {
		// permissive fallback to other base64 flavours
		if macBytes, err = tryBase64(req.Macaroon); err != nil {
			writeError(w, r, http.StatusBadRequest, ErrBadRequest, "macaroon is not valid base64", false)
			return
		}
	}
	resp := grantVerifyResp{WouldSucceed: true}
	if err := grant.VerifySignature(macBytes, s.hubID.MacaroonRootKey, grant.AlwaysSatisfied); err != nil {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, "signature_invalid: "+err.Error())
		writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
		return
	}
	parsed, err := grant.Parse(macBytes)
	if err != nil {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, "parse_failed: "+err.Error())
		writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
		return
	}
	scope := parsed.Identifier.Scope
	if scope.Primitive != "" && string(scope.Primitive) != req.HypotheticalOperation.Primitive {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, "scope.primitive does not authorize the hypothetical operation")
	}
	if scope.Namespace != "" && scope.Namespace != "*" && scope.Namespace != req.HypotheticalOperation.Namespace {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, "scope.namespace does not authorize the hypothetical operation")
	}
	if len(scope.Operations) > 0 && !contains(scope.Operations, req.HypotheticalOperation.Operation) {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, "scope.operations does not include the hypothetical operation")
	}
	if err := EvaluateCaveats(parsed.Caveats, CaveatContext{
		Now:       s.now(),
		Operation: req.HypotheticalOperation.Operation,
		Namespace: req.HypotheticalOperation.Namespace,
		Primitive: req.HypotheticalOperation.Primitive,
	}); err != nil {
		resp.WouldSucceed = false
		resp.Reasons = append(resp.Reasons, err.Error())
	}
	writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
}

// --- POST /fabric/v1/grant/revoke ---

type grantRevokeReq struct {
	GrantID string `json:"grant_id"`
	Reason  string `json:"reason"`
}

type grantRevokeResp struct {
	RevocationEventID string `json:"revocation_event_id"`
}

func (s *Server) handleGrantRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req grantRevokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.GrantID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "grant_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "grant", Operation: "revoke"}); err != nil {
		return
	}
	g := grantFromCtx(ctx)

	// Write a revocation event to a History stream named "__revocations__"
	// so other transports can subscribe (full discharge protocol lands at M3).
	const stream = "revocations"
	eventID := newULID()
	body, _ := json.Marshal(map[string]any{
		"grant_id":  req.GrantID,
		"reason":    req.Reason,
		"revoked_by": g.Identifier.IssuedByPrincipal,
		"revoked_at": s.now().UTC().Format(time.RFC3339Nano),
	})
	blobPath, err := s.store.WriteBlob(storage.HistoryNamespace, eventID, body, s.cfg.Storage.FsyncWrites)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not persist revocation event", true)
		return
	}
	in := storage.HistoryAppendInput{
		StreamID:            stream,
		Kind:                "grant.revoked",
		PayloadCiphertext:   body,
		PayloadMetadata:     string(body),
		AppendedByPrincipal: g.Identifier.IssuedByPrincipal,
		AppendedByGrantID:   g.Identifier.GrantID,
	}
	// Look up current head so we don't conflict on first revocation either.
	var prev []byte
	_ = s.store.DB().QueryRowContext(ctx, `
        SELECT COALESCE(head_event_hash, x'') FROM streams WHERE namespace = ? AND stream_id = ?`,
		storage.HistoryNamespace, stream,
	).Scan(&prev)
	in.PreviousEventHash = prev

	res, err := s.store.AppendHistoryEvent(ctx, in, blobPath, eventID)
	if err != nil {
		s.logger.Error("AppendHistoryEvent (revoke) failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "revocation persist failed", true)
		return
	}
	_ = s.store.MarkGrantRevoked(ctx, req.GrantID, res.EventID)
	writeSuccess(w, r, http.StatusOK, grantRevokeResp{RevocationEventID: res.EventID}, FreshnessNow(s.now()))
}
