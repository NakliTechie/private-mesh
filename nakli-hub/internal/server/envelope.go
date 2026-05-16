// Package server hosts the HTTP handlers, middleware, and the response
// envelope plumbing for nakli-hub. Wire shapes follow fabric-spec-001-v1.0.md.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ProtocolVersion is the wire-protocol version string sent in every response.
const ProtocolVersion = "naklimesh/1.0"

// SuccessEnvelope is the JSON shape of a successful response.
type SuccessEnvelope struct {
	OK        bool        `json:"ok"`
	Data      interface{} `json:"data"`
	Freshness *Freshness  `json:"freshness,omitempty"`
}

// ErrorEnvelope is the JSON shape of an error response.
type ErrorEnvelope struct {
	OK    bool      `json:"ok"`
	Error ErrorBody `json:"error"`
}

// ErrorBody matches the protocol error shape.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Freshness is the protocol's freshness object. For Phase 2a (no peers yet)
// we report zero peers and zero staleness.
type Freshness struct {
	AsOf         time.Time `json:"as_of"`
	PeersSynced  []string  `json:"peers_synced"`
	PeersMissing []string  `json:"peers_missing"`
	StalenessMs  int64     `json:"staleness_ms"`
}

// FreshnessNow returns a Freshness snapshot for the current moment with no peers.
func FreshnessNow(now time.Time) *Freshness {
	return &Freshness{
		AsOf:         now.UTC(),
		PeersSynced:  []string{},
		PeersMissing: []string{},
		StalenessMs:  0,
	}
}

// writeSuccess emits a SuccessEnvelope with status 200 (or status if provided).
func writeSuccess(w http.ResponseWriter, r *http.Request, status int, data interface{}, fresh *Freshness) []byte {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(SuccessEnvelope{OK: true, Data: data, Freshness: fresh})
	_, _ = w.Write(body)
	return body
}

// writeError emits an ErrorEnvelope with the appropriate HTTP status. Retryable
// is derived from the protocol's error catalogue (see fabric-spec §wire format).
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) []byte {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(ErrorEnvelope{
		OK: false,
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
	})
	_, _ = w.Write(body)
	return body
}

// Common error codes (subset; full catalogue in fabric-spec §Wire format).
const (
	ErrGrantInvalid         = "grant_invalid"
	ErrGrantMissing         = "grant_missing"
	ErrGrantRevoked         = "grant_revoked"
	ErrScopeDenied          = "scope_denied"
	ErrCaveatUnmet          = "caveat_unmet"
	ErrIdempotencyConflict  = "idempotency_conflict"
	ErrNotFound             = "not_found"
	ErrConflict             = "conflict"
	ErrUnavailable          = "unavailable"
	ErrVersionMismatch      = "version_mismatch"
	ErrBadRequest           = "bad_request"
	ErrNotImplemented       = "not_implemented"
)

// Context keys used by middleware to pass per-request data to handlers.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyGrantID
	ctxKeyPrincipal
	ctxKeyGrantBytes
	ctxKeyIdempotencyKey
)

// RequestID returns the per-request ULID assigned by middleware, or "" if
// the request is unauthenticated and the logging middleware did not run.
func RequestID(ctx context.Context) string { v, _ := ctx.Value(ctxKeyRequestID).(string); return v }

// GrantID returns the verified Grant's grant_id for the request.
func GrantID(ctx context.Context) string { v, _ := ctx.Value(ctxKeyGrantID).(string); return v }

// Principal returns the verified principal_id for the request.
func Principal(ctx context.Context) string { v, _ := ctx.Value(ctxKeyPrincipal).(string); return v }

// IdempotencyKey returns the request's X-Fabric-Idempotency-Key header value.
func IdempotencyKey(ctx context.Context) string { v, _ := ctx.Value(ctxKeyIdempotencyKey).(string); return v }
