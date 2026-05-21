package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// statusRecorder captures the HTTP status code so logMiddleware can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

// logMiddleware assigns a request id, runs the wrapped handler, then logs +
// records an operation_log row.
func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		requestID := newRequestID()
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, requestID)
		r = r.WithContext(ctx)

		w.Header().Set("X-Fabric-Version", ProtocolVersion)
		w.Header().Set("X-Fabric-Request-Id", requestID)

		sr := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(sr, r)
		if sr.status == 0 {
			sr.status = http.StatusOK
		}

		dur := s.now().Sub(start)
		grantID := GrantID(r.Context())
		principal := Principal(r.Context())

		s.logger.Info("request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sr.status,
			"duration_ms", dur.Milliseconds(),
			"grant_id_tail", tail(grantID, 8),
			"principal", principal,
		)
		if err := s.store.LogOperation(context.Background(), grantID, principal, r.Method+" "+r.URL.Path, sr.status, dur.Milliseconds(), ""); err != nil {
			s.logger.Warn("operation_log insert failed", "err", err)
		}
	})
}

// corsMiddleware emits the protocol-required CORS headers on every response.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers",
			"Content-Type, X-Fabric-Grant, X-Fabric-Idempotency-Key, X-Fabric-Request-Id, X-Fabric-Version")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-Fabric-Version, X-Fabric-Request-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware verifies the X-Fabric-Grant macaroon against the Hub's root
// HMAC key, parses any X-Fabric-Discharge headers, checks the bearer-principal
// against the retired-principal list, and surfaces the parsed Grant + the set
// of supplied discharge ids on the request context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Fabric-Grant")
		if raw == "" {
			writeError(w, r, http.StatusUnauthorized, ErrGrantMissing, "X-Fabric-Grant header missing", false)
			return
		}
		macBytes, err := decodeMacaroonHeader(raw)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, ErrGrantInvalid, "X-Fabric-Grant header is not valid base64", false)
			return
		}
		// Verify the signature chain.
		if err := grant.VerifySignature(macBytes, s.hubID.MacaroonRootKey, grant.AlwaysSatisfied); err != nil {
			writeError(w, r, http.StatusUnauthorized, ErrGrantInvalid, "macaroon signature verification failed", false)
			return
		}
		g, err := grant.Parse(macBytes)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, ErrGrantInvalid, "macaroon parse failed", false)
			return
		}
		// Refuse Grants that have been revoked.
		if revoked, _ := s.store.IsGrantRevoked(r.Context(), g.Identifier.GrantID); revoked {
			writeError(w, r, http.StatusUnauthorized, ErrGrantRevoked, "Grant has been revoked", false)
			return
		}
		// Test 30: refuse Grants whose bearer agent has been retired. The
		// requester asserts their agent-id via X-Fabric-Agent-Id; if the
		// principals row marks that agent retired, reject.
		if agentID := requesterAgentID(r); agentID != "" {
			if p, err := s.store.GetPrincipal(r.Context(), agentID); err == nil && p.RetiredAt != nil {
				writeError(w, r, http.StatusUnauthorized, ErrPrincipalRetired,
					"agent principal is retired", false)
				return
			}
		}
		// Parse and verify discharge macaroons supplied by the caller.
		dischargeIDs, derr := s.parseDischarges(r)
		if derr != nil {
			writeError(w, r, http.StatusUnauthorized, ErrGrantInvalid, derr.Error(), false)
			return
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxKeyGrantID, g.Identifier.GrantID)
		ctx = context.WithValue(ctx, ctxKeyPrincipal, g.Identifier.IssuedByPrincipal)
		ctx = context.WithValue(ctx, ctxKeyGrantBytes, macBytes)
		ctx = context.WithValue(ctx, ctxKeyGrantParsed{}, g)
		ctx = context.WithValue(ctx, ctxKeyDischargeIDs{}, dischargeIDs)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

// ctxKeyGrantParsed is a typed key (avoids collisions across packages).
type ctxKeyGrantParsed struct{}

// ctxKeyDischargeIDs is the context key for the set of verifier-ids whose
// discharge macaroons the caller successfully attached.
type ctxKeyDischargeIDs struct{}

// grantFromCtx returns the parsed Grant attached by authMiddleware.
func grantFromCtx(ctx context.Context) *grant.Grant {
	g, _ := ctx.Value(ctxKeyGrantParsed{}).(*grant.Grant)
	return g
}

// dischargeIDsFromCtx returns the set of discharge ids the caller supplied.
// Non-nil even when no discharges were attached, to simplify caller logic.
func dischargeIDsFromCtx(ctx context.Context) map[string]struct{} {
	m, _ := ctx.Value(ctxKeyDischargeIDs{}).(map[string]struct{})
	if m == nil {
		return map[string]struct{}{}
	}
	return m
}

// parseDischarges reads X-Fabric-Discharge (comma-separated base64 macaroons),
// verifies each against the Hub's macaroon root key, and returns the set of
// discharge ids (each id is the verifier URL embedded in the macaroon).
func (s *Server) parseDischarges(r *http.Request) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	raw := r.Header.Get("X-Fabric-Discharge")
	if raw == "" {
		return out, nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		mac, err := decodeMacaroonHeader(part)
		if err != nil {
			return nil, err
		}
		// Verify the discharge's signature + time caveat using AlwaysSatisfied
		// for everything except `time <`, which we evaluate manually so an
		// expired discharge is rejected here rather than at handler time.
		check := func(c string) error {
			c = strings.TrimSpace(c)
			if strings.HasPrefix(c, "time < ") {
				return checkTimeBefore(c[len("time < "):], s.now())
			}
			return nil
		}
		if err := grant.VerifySignature(mac, s.hubID.MacaroonRootKey, check); err != nil {
			return nil, err
		}
		parsed, err := grant.Parse(mac)
		if err != nil {
			return nil, err
		}
		// Discharge macaroons carry their verifier URL as the parsed id.
		// The grant.Parse path treats the id as a JSON-marshalled Identifier;
		// for discharges we mint with a synthetic Identifier whose GrantID is
		// the verifier URL.
		out[parsed.Identifier.GrantID] = struct{}{}
		// Also remember in the cache so the next request without the discharge
		// header can still pass within the staleness budget (24h default).
		s.dischargeRemember(parsed.Identifier.GrantID, mac, 24*time.Hour)
	}
	return out, nil
}

// idempotencyMiddleware implements the idempotency flow from hub-spec
// §"Idempotency flow". Replays return the stored body with HTTP 200; conflicts
// return HTTP 409. New requests proceed to the handler, and the handler is
// expected to call recordIdempotency on success.
//
// The middleware buffers the request body so the handler still sees it after
// the body has been hashed and (potentially) consulted for the idempotency
// table.
func (s *Server) idempotencyMiddleware(endpoint string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Fabric-Idempotency-Key")
		if key == "" {
			writeError(w, r, http.StatusBadRequest, ErrBadRequest, "X-Fabric-Idempotency-Key header is required", false)
			return
		}
		grantID := GrantID(r.Context())
		if grantID == "" {
			writeError(w, r, http.StatusUnauthorized, ErrGrantMissing, "Grant context missing", false)
			return
		}
		// Cap idempotency-middleware body reads so a single authenticated
		// client cannot OOM the Hub with a multi-GB POST. The cap is
		// generous: 2x MaxEventSizeBytes plus 256 KiB of headers / macaroon
		// / wrapping JSON slop — large enough that legitimate vault events
		// (already limited to MaxEventSizeBytes of ciphertext) survive
		// base64 inflation, small enough that a single request cannot
		// dominate available memory.
		const headerSlop = 256 << 10
		maxBody := s.cfg.Storage.MaxEventSizeBytes*2 + headerSlop
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// http.MaxBytesReader returns *http.MaxBytesError on overflow,
			// which writeError will translate to a normal error envelope.
			writeError(w, r, http.StatusRequestEntityTooLarge, ErrBadRequest, "request body exceeds size limit", false)
			return
		}
		_ = r.Body.Close()
		payloadHash := storage.HashPayload(body)
		res, err := s.store.LookupIdempotency(r.Context(), key, grantID, payloadHash)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "idempotency lookup failed", true)
			return
		}
		switch res.Outcome {
		case storage.IdempotencyReplay:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(res.ResponseStatus)
			_, _ = w.Write(res.ResponseBody)
			return
		case storage.IdempotencyConflict:
			writeError(w, r, http.StatusConflict, ErrIdempotencyConflict, "idempotency key reused with different payload", false)
			return
		case storage.IdempotencyFresh:
			// continue
		}

		ctx := context.WithValue(r.Context(), ctxKeyIdempotencyKey, key)
		ctx = context.WithValue(ctx, ctxKeyEndpoint{}, endpoint)
		ctx = context.WithValue(ctx, ctxKeyPayloadHash{}, payloadHash)
		// Replace body with a fresh reader pointing at our buffer.
		r = r.WithContext(ctx)
		r.Body = io.NopCloser(bytes.NewReader(body))

		// Use a buffer-backed ResponseWriter so we can persist the response
		// body for idempotent replay.
		buf := &bytes.Buffer{}
		rec := &bufferingResponseWriter{ResponseWriter: w, buf: buf}
		next.ServeHTTP(rec, r)

		if rec.status >= 200 && rec.status < 300 {
			if err := s.store.PutIdempotency(context.Background(), key, grantID, endpoint, payloadHash, rec.status, buf.Bytes(), s.cfg.Idempotency.RetentionSeconds); err != nil {
				s.logger.Warn("idempotency persist failed", "err", err, "key", key)
			}
		}
	})
}

// bufferingResponseWriter mirrors writes to an in-memory buffer so the
// idempotency middleware can persist successful responses for replay.
type bufferingResponseWriter struct {
	http.ResponseWriter
	buf    *bytes.Buffer
	status int
}

func (b *bufferingResponseWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.buf.Write(p)
	return b.ResponseWriter.Write(p)
}

func (b *bufferingResponseWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
	b.ResponseWriter.WriteHeader(code)
}

// ctxKey types for endpoint + payload hash so handlers don't recompute them.
type ctxKeyEndpoint struct{}
type ctxKeyPayloadHash struct{}

// endpointFromCtx returns the endpoint string the idempotency middleware
// stashed; "" if not present.
func endpointFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyEndpoint{}).(string)
	return v
}

// payloadHashFromCtx returns the SHA-256 of the request body.
func payloadHashFromCtx(ctx context.Context) []byte {
	v, _ := ctx.Value(ctxKeyPayloadHash{}).([]byte)
	return v
}

// decodeMacaroonHeader accepts the wire-format macaroon as base64 with optional
// padding or url-safe alphabet. The Hub is permissive on input encoding.
func decodeMacaroonHeader(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Try standard base64, then url-safe; ignore padding mismatches.
	return tryBase64(s)
}

// newRequestID returns a fresh ULID string for use as X-Fabric-Request-Id.
func newRequestID() string {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	return id.String()
}

// tail returns the last n characters of s, padding with spaces if too short.
// Used for log-friendly grant id rendering ("…trailing 8 chars" per spec).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
