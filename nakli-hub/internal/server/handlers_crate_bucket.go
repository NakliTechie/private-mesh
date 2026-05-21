// crate-agent M3 piece 1 — Hub-side bucket-proxy handlers.
//
// 7 endpoints under /v1/crate/* let the daemon use its sync-scoped capability
// against real R2 (and later B2/Hetzner/AWS S3) buckets without ever holding
// the bucket's secret access key. The Hub stores creds encrypted-at-rest
// under bucketCredsKey = HKDF(macaroon_root_key, "crate-buckets", "v1"), and
// signs sig-v4 requests on the daemon's behalf at proxy time.
//
// Auth model:
//   - register uses identity:pair (any user-authenticated principal can register
//     buckets they own).
//   - object + list + bucket-metadata use sync scope on the registered
//     bucket_id — the daemon's capability from /v1/pairing/redeem has exactly
//     this shape (scope.namespace = bucket_id).
//
// Streaming:
//   - GET / PUT stream through io.Copy — no in-memory buffering. R2 supports
//     chunked transfer; the Hub passes through.
//   - PUT signs with UNSIGNED-PAYLOAD (sig-v4 sentinel) to avoid having to
//     SHA-256 the body before forwarding — that would force buffering.
//   - HEAD / GET / LIST / DELETE sign with the empty-body SHA-256 hash —
//     universal across R2/Hetzner/AWS.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/crate"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// allowedProviders is the set of provider strings accepted by the register
// endpoint. R2 is the launch provider; B2/Hetzner/AWS S3 are wired in the
// endpoint builder + sig-v4 layer but not yet manually verified end-to-end.
var allowedProviders = map[string]struct{}{
	"r2":      {},
	"hetzner": {},
	"b2":      {},
	"aws-s3":  {},
}

// --- POST /v1/crate/bucket/register -----------------------------------------

type crateBucketRegisterReq struct {
	Provider    string `json:"provider"`     // "r2" | "b2" | "hetzner" | "aws-s3"
	AccountID   string `json:"account_id"`   // R2 only; otherwise ignored
	Region      string `json:"region"`       // "auto" for R2; datacenter for Hetzner
	BucketName  string `json:"bucket_name"`
	AccessKey   string `json:"access_key"`   // the access key ID (not secret)
	SecretKey   string `json:"secret_key"`   // sealed before persisting
	EndpointURL string `json:"endpoint_url"` // optional override; otherwise computed
}

type crateBucketRegisterResp struct {
	BucketID string `json:"bucket_id"`
}

func (s *Server) handleCrateBucketRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "pair"}); err != nil {
		return
	}

	body, err := decodePayloadBody(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, err.Error(), false)
		return
	}
	var req crateBucketRegisterReq
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if _, ok := allowedProviders[provider]; !ok {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest,
			"unsupported provider; allowed: r2, hetzner, b2, aws-s3", false)
		return
	}
	if req.BucketName == "" || req.AccessKey == "" || req.SecretKey == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest,
			"bucket_name, access_key, secret_key are required", false)
		return
	}
	if provider == "r2" && req.AccountID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest,
			"account_id is required for provider=r2", false)
		return
	}
	region := req.Region
	if region == "" {
		if provider == "r2" {
			region = "auto"
		} else {
			writeError(w, r, http.StatusBadRequest, ErrBadRequest,
				"region is required for provider="+provider, false)
			return
		}
	}

	endpoint := req.EndpointURL
	if endpoint == "" {
		built, ok := crate.EndpointForProvider(provider, req.AccountID, region, req.BucketName)
		if !ok {
			writeError(w, r, http.StatusBadRequest, ErrBadRequest,
				"could not build endpoint URL for provider="+provider, false)
			return
		}
		endpoint = built
	}

	// Mint a fresh bucket_id (ULID — collision-resistant) and seal the secret.
	bucketID := "bk_" + newULID()
	credsKey, err := crate.DeriveBucketCredsKey(s.hubID.MacaroonRootKey)
	if err != nil {
		s.logger.Error("DeriveBucketCredsKey failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "key derivation failed", true)
		return
	}
	sealed, nonce, err := crate.SealSecret(credsKey, []byte(req.SecretKey), bucketID)
	if err != nil {
		s.logger.Error("SealSecret failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "seal failed", true)
		return
	}

	g := grantFromCtx(ctx)
	row := storage.CrateBucket{
		BucketID:              bucketID,
		Provider:              provider,
		AccountID:             req.AccountID,
		Region:                region,
		BucketName:            req.BucketName,
		EndpointURL:           endpoint,
		AccessKeyID:           req.AccessKey,
		SecretAccessKeySealed: sealed,
		Nonce:                 nonce,
		RegisteredByPrincipal: g.Identifier.IssuedByPrincipal,
		CreatedAt:             s.now().UTC(),
	}
	if err := s.store.CreateCrateBucket(ctx, row); err != nil {
		s.logger.Error("CreateCrateBucket failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not store bucket registration", true)
		return
	}
	writeSuccess(w, r, http.StatusCreated, crateBucketRegisterResp{BucketID: bucketID}, FreshnessNow(s.now()))
}

// --- GET /v1/crate/bucket/{bucket_id} ---------------------------------------

type crateBucketMetadataResp struct {
	BucketID    string  `json:"bucket_id"`
	Provider    string  `json:"provider"`
	Region      string  `json:"region"`
	BucketName  string  `json:"bucket_name"`
	EndpointURL string  `json:"endpoint_url"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
}

func (s *Server) handleCrateBucketMetadata(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bucketID := r.PathValue("bucket_id")
	if bucketID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "bucket_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "sync",
		Namespace: bucketID,
		Operation: "read",
	}); err != nil {
		return
	}
	b, err := s.store.LookupCrateBucket(ctx, bucketID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, ErrNotFound, "no bucket matches that bucket_id", false)
			return
		}
		s.logger.Error("LookupCrateBucket failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "bucket lookup failed", true)
		return
	}
	resp := crateBucketMetadataResp{
		BucketID:    b.BucketID,
		Provider:    b.Provider,
		Region:      b.Region,
		BucketName:  b.BucketName,
		EndpointURL: b.EndpointURL,
		CreatedAt:   b.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if b.LastUsedAt != nil {
		ts := b.LastUsedAt.UTC().Format(time.RFC3339Nano)
		resp.LastUsedAt = &ts
	}
	writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
}

// --- GET /v1/crate/bucket -------------------------------------------------
//
// Lists all buckets registered by the calling principal. Used by the future
// nakliOS "Connect Crate" Settings panel to surface "your buckets" in a
// picker; also useful for `nakli-cli crate-bucket list` (deferred to M5+).
// Auth: identity:pair (same as register — the principal owns these rows).

type crateBucketListResp struct {
	Buckets []crateBucketMetadataResp `json:"buckets"`
}

func (s *Server) handleCrateBucketList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "pair"}); err != nil {
		return
	}
	g := grantFromCtx(ctx)
	rows, err := s.store.ListCrateBucketsByPrincipal(ctx, g.Identifier.IssuedByPrincipal)
	if err != nil {
		s.logger.Error("ListCrateBucketsByPrincipal failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "bucket list failed", true)
		return
	}
	out := crateBucketListResp{Buckets: make([]crateBucketMetadataResp, 0, len(rows))}
	for _, b := range rows {
		m := crateBucketMetadataResp{
			BucketID:    b.BucketID,
			Provider:    b.Provider,
			Region:      b.Region,
			BucketName:  b.BucketName,
			EndpointURL: b.EndpointURL,
			CreatedAt:   b.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if b.LastUsedAt != nil {
			ts := b.LastUsedAt.UTC().Format(time.RFC3339Nano)
			m.LastUsedAt = &ts
		}
		out.Buckets = append(out.Buckets, m)
	}
	writeSuccess(w, r, http.StatusOK, out, FreshnessNow(s.now()))
}

// --- HEAD / GET / PUT / DELETE /v1/crate/object/{bucket_id}/{path...} -------

func (s *Server) handleCrateObject(w http.ResponseWriter, r *http.Request) {
	bucketID := r.PathValue("bucket_id")
	if bucketID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "bucket_id is required", false)
		return
	}
	// The {path...} wildcard captures everything after the bucket_id segment.
	objectPath := r.PathValue("path")
	if objectPath == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "object path is required", false)
		return
	}

	op := "read"
	if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		op = "write"
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "sync",
		Namespace: bucketID,
		Operation: op,
	}); err != nil {
		return
	}

	b, err := s.loadAndDecrypt(r.Context(), bucketID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, ErrNotFound, "no bucket matches that bucket_id", false)
			return
		}
		s.logger.Error("loadAndDecrypt failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "bucket lookup or decrypt failed", true)
		return
	}
	defer zero(b.secretAccessKey)

	upstreamURL := b.row.EndpointURL + objectPath
	s.proxyToUpstream(w, r, b, upstreamURL, r.Method == http.MethodPut /* stream-payload PUT */)
}

// --- GET /v1/crate/list/{bucket_id} -----------------------------------------

func (s *Server) handleCrateList(w http.ResponseWriter, r *http.Request) {
	bucketID := r.PathValue("bucket_id")
	if bucketID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "bucket_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "sync",
		Namespace: bucketID,
		Operation: "read",
	}); err != nil {
		return
	}
	b, err := s.loadAndDecrypt(r.Context(), bucketID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, ErrNotFound, "no bucket matches that bucket_id", false)
			return
		}
		s.logger.Error("loadAndDecrypt failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "bucket lookup or decrypt failed", true)
		return
	}
	defer zero(b.secretAccessKey)

	// Translate our LIST query params to the S3 v2 LIST API.
	// list-type=2 + prefix + continuation-token + max-keys.
	q := url.Values{}
	q.Set("list-type", "2")
	if p := r.URL.Query().Get("prefix"); p != "" {
		q.Set("prefix", p)
	}
	if ct := r.URL.Query().Get("continuation_token"); ct != "" {
		q.Set("continuation-token", ct)
	}
	if mk := r.URL.Query().Get("max_keys"); mk != "" {
		q.Set("max-keys", mk)
	}

	// S3 LIST: GET on the bucket base URL with query.
	upstreamURL := b.row.EndpointURL + "?" + q.Encode()
	r2 := r.Clone(r.Context())
	r2.Method = http.MethodGet
	s.proxyToUpstream(w, r2, b, upstreamURL, false /* read */)
}

// --- shared plumbing --------------------------------------------------------

// loadedBucket carries the looked-up row PLUS the decrypted secret access key
// (held in memory just long enough to sign the upstream request).
type loadedBucket struct {
	row             *storage.CrateBucket
	secretAccessKey []byte
}

func (s *Server) loadAndDecrypt(ctx context.Context, bucketID string) (*loadedBucket, error) {
	b, err := s.store.LookupCrateBucket(ctx, bucketID)
	if err != nil {
		return nil, err
	}
	credsKey, err := crate.DeriveBucketCredsKey(s.hubID.MacaroonRootKey)
	if err != nil {
		return nil, fmt.Errorf("derive: %w", err)
	}
	plain, err := crate.OpenSecret(credsKey, b.SecretAccessKeySealed, b.Nonce, b.BucketID)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return &loadedBucket{row: b, secretAccessKey: plain}, nil
}

// proxyToUpstream signs the upstream request, forwards the body (streaming),
// and copies the response back to the client (status + safe headers + body).
//
// streamPayload=true (PUT) → sign with UNSIGNED-PAYLOAD to skip buffering.
// streamPayload=false → sign with empty-body SHA-256 hash.
func (s *Server) proxyToUpstream(
	w http.ResponseWriter,
	r *http.Request,
	b *loadedBucket,
	upstreamURL string,
	streamPayload bool,
) {
	ctx := r.Context()

	// Build the upstream request.
	upstream, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		s.logger.Error("upstream NewRequest failed", "err", err, "url", upstreamURL)
		writeError(w, r, http.StatusBadGateway, ErrUnavailable, "upstream request setup failed", true)
		return
	}

	// Propagate Content-Type / Content-Length on PUT. Also propagate
	// conditional headers (If-Match / If-None-Match) on PUT/DELETE — R2
	// uses these for concurrent-write safety (see crate browser M6.x).
	// We don't propagate If-Modified-Since for GET because we have no
	// users of it yet; can add if needed.
	if r.Method == http.MethodPut {
		if ct := r.Header.Get("Content-Type"); ct != "" {
			upstream.Header.Set("Content-Type", ct)
		}
		if r.ContentLength > 0 {
			upstream.ContentLength = r.ContentLength
		}
	}
	if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		if v := r.Header.Get("If-Match"); v != "" {
			upstream.Header.Set("If-Match", v)
		}
		if v := r.Header.Get("If-None-Match"); v != "" {
			upstream.Header.Set("If-None-Match", v)
		}
	}

	// Sign.
	payloadHash := ""
	if streamPayload {
		payloadHash = crate.UnsignedPayload
	}
	signedHeaders, serr := crate.SignRequest(crate.SignOpts{
		Method:      r.Method,
		URL:         upstreamURL,
		Headers:     upstream.Header,
		PayloadHash: payloadHash,
		Region:      b.row.Region,
		Service:     "s3",
		AccessKey:   b.row.AccessKeyID,
		SecretKey:   string(b.secretAccessKey),
	})
	if serr != nil {
		s.logger.Error("sign upstream failed", "err", serr)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "sig-v4 sign failed", true)
		return
	}
	upstream.Header = signedHeaders
	upstream.Host = signedHeaders.Get("Host")

	// Send.
	resp, derr := s.httpClient().Do(upstream)
	if derr != nil {
		s.logger.Error("upstream Do failed", "err", derr, "url", upstreamURL)
		writeError(w, r, http.StatusBadGateway, ErrUnavailable, "upstream call failed: "+derr.Error(), true)
		return
	}
	defer resp.Body.Close()

	// Best-effort: touch last_used_at on any 2xx.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if terr := s.store.TouchCrateBucketLastUsed(ctx, b.row.BucketID); terr != nil {
			s.logger.Warn("TouchCrateBucketLastUsed failed", "err", terr)
		}
	}

	// Copy safe response headers.
	for _, h := range passThroughResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, cerr := io.Copy(w, resp.Body); cerr != nil {
		// Headers + status already sent; we can only log.
		s.logger.Warn("response body copy failed", "err", cerr)
	}
}

// passThroughResponseHeaders is the conservative allow-list of upstream
// response headers the proxy mirrors back to the client. Includes ETag (needed
// by the daemon's manifest reconciliation), Content-Type, Content-Length,
// Content-Range (range requests), Last-Modified, Accept-Ranges, and the
// canonical x-amz-* set the S3 API uses for object metadata.
var passThroughResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"ETag",
	"Last-Modified",
	"x-amz-version-id",
	"x-amz-request-id",
	"x-amz-id-2",
	"x-amz-meta-crate-iv",
}

// httpClient returns the Hub's outbound HTTP client. Centralized so tests can
// stub via s.bucketProxyClient when needed; default is http.DefaultClient.
func (s *Server) httpClient() *http.Client {
	if s.bucketProxyClient != nil {
		return s.bucketProxyClient
	}
	return http.DefaultClient
}

// zero overwrites a byte slice. Used to wipe decrypted secrets after the
// upstream request completes.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
