package server

import (
	"context"
	"errors"
	"net/http"
)

// scopeRequirement is the per-handler authorization request.
type scopeRequirement struct {
	Primitive string
	Namespace string
	Operation string
	// IsDelegation is true on /grant/mint when a parent grant is present;
	// drives the nondelegatable caveat enforcement.
	IsDelegation bool
	// Bridge-only fields populated by the bridge handler when it needs the
	// bridge-specific caveats (`max-amount`, `only-domain`,
	// `requires-human-approval`) evaluated.
	IsBridgeCall   bool
	BridgeDomain   string
	BridgeAmount   int64
	BridgeCurrency string
}

// checkAuth performs scope + caveat enforcement against the parsed Grant on
// the request context. Returns nil on success. On failure it writes the
// appropriate error response and returns a sentinel; handlers should return
// immediately when this returns non-nil.
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request, req scopeRequirement) error {
	g := grantFromCtx(r.Context())
	if g == nil {
		writeError(w, r, http.StatusUnauthorized, ErrGrantMissing, "Grant context missing", false)
		return errAuthShortCircuited
	}
	scope := g.Identifier.Scope
	if scope.Primitive != "" && string(scope.Primitive) != req.Primitive {
		writeError(w, r, http.StatusForbidden, ErrScopeDenied,
			"Grant scope primitive does not authorize this operation", false)
		return errAuthShortCircuited
	}
	if req.Namespace != "" && scope.Namespace != "" && scope.Namespace != "*" && scope.Namespace != req.Namespace {
		writeError(w, r, http.StatusForbidden, ErrScopeDenied,
			"Grant scope namespace does not authorize this stream", false)
		return errAuthShortCircuited
	}
	if req.Operation != "" && len(scope.Operations) > 0 && !contains(scope.Operations, req.Operation) {
		writeError(w, r, http.StatusForbidden, ErrScopeDenied,
			"Grant scope operations do not include this request's operation", false)
		return errAuthShortCircuited
	}

	cctx := CaveatContext{
		Now:                    s.now(),
		Operation:              req.Operation,
		Namespace:              req.Namespace,
		Primitive:              req.Primitive,
		HasIdempotencyKey:      IdempotencyKey(r.Context()) != "",
		IsDelegationRequest:    req.IsDelegation,
		GrantID:                g.Identifier.GrantID,
		RequesterPrincipalType: requesterPrincipalType(r),
		RequesterAgentID:       requesterAgentID(r),
		RequesterDeviceID:      requesterDeviceID(r),
		IsBridgeCall:           req.IsBridgeCall,
		BridgeDomain:           req.BridgeDomain,
		BridgeAmount:           req.BridgeAmount,
		BridgeCurrency:         req.BridgeCurrency,
		Server:                 s,
		DischargeIDs:           dischargeIDsFromCtx(r.Context()),
		StrictBinding:          s.cfg.Auth.StrictCaveatBinding,
	}
	if err := EvaluateCaveats(g.Caveats, cctx); err != nil {
		var ce *CaveatError
		if errors.As(err, &ce) {
			switch ce.Kind {
			case KindRateLimited:
				writeError(w, r, http.StatusTooManyRequests, ErrRateLimited, ce.Error(), true)
				return errAuthShortCircuited
			case KindHumanApproval:
				// Caller (bridge handler) handles the 202 + pending_id flow.
				return errHumanApprovalRequired
			default:
				writeError(w, r, http.StatusForbidden, ErrCaveatUnmet, ce.Error(), false)
				return errAuthShortCircuited
			}
		}
		writeError(w, r, http.StatusForbidden, ErrCaveatUnmet, err.Error(), false)
		return errAuthShortCircuited
	}
	return nil
}

// requireGrantOwnership rejects the request unless the authenticated
// principal is either the issuer or the recipient of the target grant.
// Used by /grant/revoke and DELETE /v1/capability/{id} to prevent a
// holder of an unrelated `grant:revoke` grant from revoking other
// principals' capabilities.
//
// On not-found (the Hub has never seen this grant), the request is
// rejected as `not_found` — fail closed. On any DB error, returns 500.
// On a successful check, returns nil and the caller proceeds.
func (s *Server) requireGrantOwnership(w http.ResponseWriter, r *http.Request, targetGrantID string) error {
	g := grantFromCtx(r.Context())
	if g == nil {
		writeError(w, r, http.StatusUnauthorized, ErrGrantMissing, "Grant context missing", false)
		return errAuthShortCircuited
	}
	known, ok, err := s.store.GetKnownGrant(r.Context(), targetGrantID)
	if err != nil {
		s.logger.Error("GetKnownGrant failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not look up grant", true)
		return errAuthShortCircuited
	}
	if !ok {
		// The Hub never recorded this grant — revoking it would be a
		// no-op on the auth side anyway, but we don't want to leak that
		// the id was unknown vs. unauthorized either. 404 keeps both
		// outcomes indistinguishable to a probing attacker.
		writeError(w, r, http.StatusNotFound, ErrNotFound, "grant not found", false)
		return errAuthShortCircuited
	}
	requester := g.Identifier.IssuedByPrincipal
	if requester != known.IssuedByPrincipal && requester != known.RecipientPrincipal {
		writeError(w, r, http.StatusForbidden, ErrScopeDenied,
			"requester is neither the issuer nor the recipient of the target grant", false)
		return errAuthShortCircuited
	}
	return nil
}

// errAuthShortCircuited is a sentinel; handlers use it to detect that checkAuth
// already wrote the response.
var errAuthShortCircuited = errors.New("server: auth check short-circuited")

// errHumanApprovalRequired signals that the bridge handler should respond 202
// + create a pending_bridge row rather than treat the caveat as a hard reject.
var errHumanApprovalRequired = errors.New("server: human approval required")

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// pickPrincipalType returns the principal type asserted by any of the Grant's
// caveats, or "" if no assertion is present. Useful for handlers that need to
// know what kind of principal is acting (e.g., the `human` requirement on
// `/bridge/approve`).
func pickPrincipalType(caveats []string) string {
	for _, c := range caveats {
		const pfx = "principal-type in "
		if len(c) > len(pfx) && c[:len(pfx)] == pfx {
			for _, t := range parseListBracket(c[len(pfx):]) {
				return t
			}
		}
	}
	return ""
}

// stripFabricSuffix returns the bare ULID part of a principal id. The
// `@<fabric-id>` suffix is silently dropped (forward-compat hook 5).
func stripFabricSuffix(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '@' {
			return id[:i]
		}
	}
	return id
}

func requesterPrincipalType(r *http.Request) string {
	return r.Header.Get("X-Fabric-Principal-Type")
}

func requesterAgentID(r *http.Request) string {
	return r.Header.Get("X-Fabric-Agent-Id")
}

func requesterDeviceID(r *http.Request) string {
	return r.Header.Get("X-Fabric-Device-Id")
}

// _ is referenced from middleware.go in places where it makes the intent of
// "this is the value we are about to discard" clearer; keep import surface
// minimal.
var _ = context.Background
