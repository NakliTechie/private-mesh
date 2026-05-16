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
		Now:                 s.now(),
		Operation:           req.Operation,
		Namespace:           req.Namespace,
		Primitive:           req.Primitive,
		HasIdempotencyKey:   IdempotencyKey(r.Context()) != "",
		IsDelegationRequest: req.IsDelegation,
	}
	if err := EvaluateCaveats(g.Caveats, cctx); err != nil {
		var ce *CaveatError
		if errors.As(err, &ce) {
			writeError(w, r, http.StatusForbidden, ErrCaveatUnmet, ce.Error(), false)
			return errAuthShortCircuited
		}
		writeError(w, r, http.StatusForbidden, ErrCaveatUnmet, err.Error(), false)
		return errAuthShortCircuited
	}
	return nil
}

// errAuthShortCircuited is a sentinel; handlers use it to detect that checkAuth
// already wrote the response.
var errAuthShortCircuited = errors.New("server: auth check short-circuited")

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

// _ is referenced from middleware.go in places where it makes the intent of
// "this is the value we are about to discard" clearer; keep import surface
// minimal.
var _ = context.Background
