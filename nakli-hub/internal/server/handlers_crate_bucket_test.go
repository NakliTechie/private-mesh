// crate-agent M3 piece 1 — Hub-side bucket-proxy conformance tests.
//
// fakeR2 is a minimal in-process S3-API stub: HEAD/GET/PUT/DELETE on
// /{bucket}/{key} and GET /{bucket}/?list-type=2 with prefix + continuation.
// It does NOT verify sig-v4 signatures (the unit test in sigv4_test.go covers
// signing); it just stores bytes in a map and returns canonical S3 responses.
//
// The Hub is wired with httpClient = fakeR2's client so that
// proxyToUpstream's outbound call lands here instead of cloudflarestorage.com.

package server_test

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// ---------- fakeR2 ---------------------------------------------------------

type fakeR2 struct {
	mu      sync.Mutex
	objects map[string][]byte // key = "{bucket}/{key}"
	ts      *httptest.Server
}

func newFakeR2(t *testing.T) *fakeR2 {
	t.Helper()
	f := &fakeR2{objects: map[string][]byte{}}
	f.ts = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.ts.Close)
	return f
}

// serve dispatches the 5 fake S3 verbs we need. The bucket name is the first
// path segment; everything after that is the object key (or empty for LIST).
func (f *fakeR2) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	bucket := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	if bucket == "" {
		http.Error(w, "no bucket", http.StatusBadRequest)
		return
	}

	// LIST: GET /{bucket}/?list-type=2
	if r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2" {
		f.handleList(w, r, bucket)
		return
	}

	// Per-object verbs.
	switch r.Method {
	case http.MethodHead:
		f.handleHead(w, bucket, key)
	case http.MethodGet:
		f.handleGet(w, bucket, key)
	case http.MethodPut:
		f.handlePut(w, r, bucket, key)
	case http.MethodDelete:
		f.handleDelete(w, bucket, key)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (f *fakeR2) handleHead(w http.ResponseWriter, bucket, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[bucket+"/"+key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(b)))
	w.Header().Set("ETag", fmt.Sprintf("%q", fakeETag(b)))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeR2) handleGet(w http.ResponseWriter, bucket, key string) {
	f.mu.Lock()
	b, ok := f.objects[bucket+"/"+key]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(b)))
	w.Header().Set("ETag", fmt.Sprintf("%q", fakeETag(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (f *fakeR2) handlePut(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f.mu.Lock()
	f.objects[bucket+"/"+key] = body
	f.mu.Unlock()
	w.Header().Set("ETag", fmt.Sprintf("%q", fakeETag(body)))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeR2) handleDelete(w http.ResponseWriter, bucket, key string) {
	f.mu.Lock()
	delete(f.objects, bucket+"/"+key)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

type listEntry struct {
	XMLName      xml.Name `xml:"Contents"`
	Key          string   `xml:"Key"`
	Size         int      `xml:"Size"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified"`
}

type listResult struct {
	XMLName               xml.Name    `xml:"ListBucketResult"`
	IsTruncated           bool        `xml:"IsTruncated"`
	Contents              []listEntry `xml:"Contents"`
	NextContinuationToken string      `xml:"NextContinuationToken,omitempty"`
}

func (f *fakeR2) handleList(w http.ResponseWriter, r *http.Request, bucket string) {
	prefix := r.URL.Query().Get("prefix")
	f.mu.Lock()
	defer f.mu.Unlock()
	result := listResult{}
	for k, v := range f.objects {
		if !strings.HasPrefix(k, bucket+"/") {
			continue
		}
		objKey := strings.TrimPrefix(k, bucket+"/")
		if prefix != "" && !strings.HasPrefix(objKey, prefix) {
			continue
		}
		result.Contents = append(result.Contents, listEntry{
			Key:          objKey,
			Size:         len(v),
			ETag:         fmt.Sprintf("%q", fakeETag(v)),
			LastModified: time.Now().UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

func fakeETag(b []byte) string {
	// Deterministic-ish but not real MD5; tests don't validate the value
	// itself, just that it's a non-empty quoted string.
	return fmt.Sprintf("etag-%x", len(b))
}

// ---------- helpers --------------------------------------------------------

// withFakeR2 swaps the Hub's bucket-proxy HTTP client to one whose transport
// rewrites the upstream URL's scheme + host to point at the fake R2 server.
// This lets us register a bucket with the real-looking R2 endpoint URL
// (e.g. https://62231…r2.cloudflarestorage.com/) and have the actual outbound
// call hit our fake.
func (h *hubFixture) withFakeR2(t *testing.T, f *fakeR2) {
	t.Helper()
	rt := &rewriteTransport{base: http.DefaultTransport, target: f.ts.URL}
	h.srv.SetBucketProxyClient(&http.Client{Transport: rt, Timeout: 5 * time.Second})
}

type rewriteTransport struct {
	base   http.RoundTripper
	target string // e.g. "http://127.0.0.1:54321"
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(rt.target)
	// Preserve path + query; swap scheme + host.
	newURL := *req.URL
	newURL.Scheme = target.Scheme
	newURL.Host = target.Host
	req2 := req.Clone(req.Context())
	req2.URL = &newURL
	req2.Host = target.Host
	return rt.base.RoundTrip(req2)
}

func (h *hubFixture) mintCrateBucketRegisterGrant(t *testing.T) string {
	t.Helper()
	return h.mintGrantWithScope(t, grant.Primitive("identity"), "*", []string{"pair"}, nil)
}

func (h *hubFixture) mintCrateSyncGrant(t *testing.T, bucketID string, ops []string) string {
	t.Helper()
	return h.mintGrantWithScope(t, grant.Primitive("sync"), bucketID, ops, nil)
}

// registerBucket POSTs /v1/crate/bucket/register and returns the bucket_id.
// Uses "test-bucket" against the fake R2 host so the endpoint URL we register
// is the same path-style URL the proxy will then sign + forward.
func (h *hubFixture) registerBucket(t *testing.T, bucketName, accessKey, secretKey string) string {
	t.Helper()
	g := h.mintCrateBucketRegisterGrant(t)
	return h.registerBucketWithGrant(t, bucketName, accessKey, secretKey, g)
}

// registerBucketWithGrant is like registerBucket but takes a caller-supplied
// Grant — used in the list test so multiple register calls share a single
// principal.
func (h *hubFixture) registerBucketWithGrant(t *testing.T, bucketName, accessKey, secretKey, g string) string {
	t.Helper()
	// Override the endpoint_url so it lands on the rewriteTransport's path-style
	// behavior — we use a real-looking R2 URL; the transport rewrites to fake.
	endpoint := "https://62231b040ed00c96cdcf3a4541eab958.r2.cloudflarestorage.com/" + bucketName + "/"
	status, body := h.do(t, "POST", "/v1/crate/bucket/register", map[string]any{
		"provider":     "r2",
		"account_id":   "62231b040ed00c96cdcf3a4541eab958",
		"region":       "auto",
		"bucket_name":  bucketName,
		"access_key":   accessKey,
		"secret_key":   secretKey,
		"endpoint_url": endpoint,
	}, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusCreated {
		t.Fatalf("register: status=%d body=%s", status, body)
	}
	var env successEnv
	if err := jsonUnmarshalTest(body, &env); err != nil {
		t.Fatalf("register parse env: %v body=%s", err, body)
	}
	var data struct {
		BucketID string `json:"bucket_id"`
	}
	if err := jsonUnmarshalTest(env.Data, &data); err != nil {
		t.Fatalf("register parse data: %v", err)
	}
	if data.BucketID == "" {
		t.Fatalf("register: empty bucket_id; body=%s", body)
	}
	return data.BucketID
}

// jsonUnmarshalTest is a tiny test helper to avoid a top-level json import
// just for the test-only wiring.
func jsonUnmarshalTest(b []byte, v interface{}) error {
	return jsonUnmarshalInternal(b, v)
}

// ---------- handler tests -------------------------------------------------

func TestCrateBucket_RegisterAndMetadata(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	bucketID := h.registerBucket(t, "test-bucket", "ak", "sk")

	g := h.mintCrateSyncGrant(t, bucketID, []string{"read", "write"})
	status, body := h.do(t, "GET", "/v1/crate/bucket/"+bucketID, nil, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusOK {
		t.Fatalf("metadata: status=%d body=%s", status, body)
	}
	if !bytes.Contains(body, []byte(`"provider":"r2"`)) {
		t.Errorf("metadata missing provider: %s", body)
	}
	if !bytes.Contains(body, []byte(`"bucket_name":"test-bucket"`)) {
		t.Errorf("metadata missing bucket_name: %s", body)
	}
}

func TestCrateBucket_Register_RejectsMissingFields(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintCrateBucketRegisterGrant(t)
	status, body := h.do(t, "POST", "/v1/crate/bucket/register", map[string]any{
		"provider":    "r2",
		"account_id":  "62231b040ed00c96cdcf3a4541eab958",
		"region":      "auto",
		"bucket_name": "x",
		// access_key + secret_key missing.
	}, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", status, body)
	}
}

func TestCrateBucket_Register_RejectsBadProvider(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintCrateBucketRegisterGrant(t)
	status, _ := h.do(t, "POST", "/v1/crate/bucket/register", map[string]any{
		"provider":    "wasabi",
		"region":      "us-east-1",
		"bucket_name": "x",
		"access_key":  "a",
		"secret_key":  "b",
	}, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d", status)
	}
}

func TestCrateBucket_Metadata_404OnUnknown(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintCrateSyncGrant(t, "bk_does_not_exist", []string{"read"})
	status, _ := h.do(t, "GET", "/v1/crate/bucket/bk_does_not_exist", nil, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
}

func TestCrateBucket_PutGetHeadDeleteRoundTrip(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	bucketID := h.registerBucket(t, "test-bucket", "ak", "sk")
	g := h.mintCrateSyncGrant(t, bucketID, []string{"read", "write"})

	objectPath := "folder/file.txt"
	payload := []byte("hello from crate-agent\n")

	// PUT
	status, body := h.doRaw(t, "PUT", "/v1/crate/object/"+bucketID+"/"+objectPath,
		bytes.NewReader(payload), map[string]string{
			"X-Fabric-Grant": g,
			"Content-Type":   "text/plain",
		})
	if status != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", status, body)
	}

	// HEAD
	status, _ = h.doRaw(t, "HEAD", "/v1/crate/object/"+bucketID+"/"+objectPath, nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("HEAD: status=%d", status)
	}

	// GET — verifies body integrity through proxy
	status, body = h.doRaw(t, "GET", "/v1/crate/object/"+bucketID+"/"+objectPath, nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("GET: status=%d", status)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("GET body mismatch:\n got:  %q\n want: %q", body, payload)
	}

	// LIST
	status, body = h.do(t, "GET", "/v1/crate/list/"+bucketID+"?prefix=folder/", nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusOK {
		t.Fatalf("LIST: status=%d body=%s", status, body)
	}
	if !bytes.Contains(body, []byte("folder/file.txt")) {
		t.Errorf("LIST missing key: %s", body)
	}

	// DELETE
	status, _ = h.doRaw(t, "DELETE", "/v1/crate/object/"+bucketID+"/"+objectPath, nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d", status)
	}

	// GET after delete — 404 (R2 + S3 return 404 NoSuchKey).
	status, _ = h.doRaw(t, "GET", "/v1/crate/object/"+bucketID+"/"+objectPath, nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusNotFound {
		t.Errorf("GET after delete: expected 404, got %d", status)
	}
}

func TestCrateBucket_Object_WrongScope(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	bucketID := h.registerBucket(t, "test-bucket", "ak", "sk")
	// Grant scoped to a DIFFERENT bucket_id — should be rejected with 403.
	wrongG := h.mintCrateSyncGrant(t, "bk_other_bucket", []string{"read", "write"})
	status, _ := h.do(t, "GET", "/v1/crate/object/"+bucketID+"/x", nil,
		map[string]string{"X-Fabric-Grant": wrongG})
	if status != http.StatusForbidden {
		t.Errorf("wrong-scope GET: expected 403, got %d", status)
	}
}

func TestCrateBucket_Object_ReadOnlyCannotPut(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	bucketID := h.registerBucket(t, "test-bucket", "ak", "sk")
	roG := h.mintCrateSyncGrant(t, bucketID, []string{"read"})
	status, _ := h.doRaw(t, "PUT", "/v1/crate/object/"+bucketID+"/x.txt",
		bytes.NewReader([]byte("nope")), map[string]string{"X-Fabric-Grant": roG})
	if status != http.StatusForbidden {
		t.Errorf("read-only PUT: expected 403, got %d", status)
	}
}

func TestCrateBucket_Object_UnknownBucket404(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	g := h.mintCrateSyncGrant(t, "bk_missing", []string{"read"})
	status, _ := h.do(t, "GET", "/v1/crate/object/bk_missing/x.txt", nil,
		map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusNotFound {
		t.Errorf("unknown bucket GET: expected 404, got %d", status)
	}
}

// TestCrateBucket_List verifies GET /v1/crate/bucket returns all buckets
// registered by the calling principal — used by future nakliOS Settings
// "your buckets" UI.
//
// Critical: ALL THREE calls (two registers + one list) reuse the SAME
// Grant so they share a principal. The list endpoint filters by
// principal — different grants → different principals → empty list.
func TestCrateBucket_List(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	g := h.mintCrateBucketRegisterGrant(t)
	id1 := h.registerBucketWithGrant(t, "alpha-bucket", "ak1", "sk1", g)
	id2 := h.registerBucketWithGrant(t, "beta-bucket", "ak2", "sk2", g)

	status, body := h.do(t, "GET", "/v1/crate/bucket", nil, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", status, body)
	}
	for _, want := range []string{id1, id2, `"provider":"r2"`, `"region":"auto"`, `"bucket_name":"alpha-bucket"`, `"bucket_name":"beta-bucket"`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("list body missing %q; got %s", want, body)
		}
	}
}

// TestCrateBucket_List_ScopesByPrincipal confirms that the list endpoint
// only returns buckets owned by the calling principal, not someone else's.
func TestCrateBucket_List_ScopesByPrincipal(t *testing.T) {
	h := newHubFixture(t)
	f := newFakeR2(t)
	h.withFakeR2(t, f)

	g1 := h.mintCrateBucketRegisterGrant(t)
	g2 := h.mintCrateBucketRegisterGrant(t)
	id1 := h.registerBucketWithGrant(t, "alpha-bucket", "ak1", "sk1", g1)
	_ = h.registerBucketWithGrant(t, "beta-bucket", "ak2", "sk2", g2)

	status, body := h.do(t, "GET", "/v1/crate/bucket", nil, map[string]string{
		"X-Fabric-Grant": g1,
	})
	if status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", status, body)
	}
	if !bytes.Contains(body, []byte(id1)) {
		t.Errorf("list should include caller's bucket %s; got %s", id1, body)
	}
	if bytes.Contains(body, []byte(`"bucket_name":"beta-bucket"`)) {
		t.Errorf("list should NOT include other principal's bucket; got %s", body)
	}
}
