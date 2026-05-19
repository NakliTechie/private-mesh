package crate

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/crypto"
)

// AWS publishes a sig-v4 reference test suite ("get-vanilla", "get-utf8", etc.)
// but those use the IAM `service` and dummy credentials. We use a known
// example with the AWS-published credentials from the docs to confirm the
// canonical-string + signing-key pipeline reproduces byte-identical results.
//
// Test pinned to:
//   - access key: AKIAIOSFODNN7EXAMPLE
//   - secret key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
//   - region:     us-east-1
//   - service:    s3
//   - date:       2013-05-24T00:00:00Z
//
// Reference: https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustDate(%q): %v", s, err)
	}
	return d
}

// TestSignRequest_GetEmpty verifies a sig-v4 signature against the AWS-published
// example for an empty GET. The expected Authorization header comes from the
// docs page above.
func TestSignRequest_GetEmpty(t *testing.T) {
	headers, err := SignRequest(SignOpts{
		Method:    http.MethodGet,
		URL:       "https://examplebucket.s3.amazonaws.com/test.txt",
		Region:    "us-east-1",
		Service:   "s3",
		AccessKey: testAccessKey,
		SecretKey: testSecretKey,
		Date:      mustDate(t, "2013-05-24T00:00:00Z"),
		Headers: http.Header{
			"Range": []string{"bytes=0-9"},
		},
	})
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	// Expected per AWS docs example "GET Object".
	wantAuth := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := headers.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization mismatch:\n got:  %s\n want: %s", got, wantAuth)
	}

	// Sanity-check the always-added headers.
	if got := headers.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("X-Amz-Date = %q, want 20130524T000000Z", got)
	}
	if got := headers.Get("X-Amz-Content-Sha256"); got != emptyBodySHA256 {
		t.Errorf("X-Amz-Content-Sha256 = %q, want emptyBodySHA256", got)
	}
	if got := headers.Get("Host"); got != "examplebucket.s3.amazonaws.com" {
		t.Errorf("Host = %q, want examplebucket.s3.amazonaws.com", got)
	}
}

// TestSignRequest_HeadNoBody checks a HEAD with no caller-supplied headers
// signs successfully and produces the expected payload-hash header.
func TestSignRequest_HeadNoBody(t *testing.T) {
	headers, err := SignRequest(SignOpts{
		Method:    http.MethodHead,
		URL:       "https://62231b040ed00c96cdcf3a4541eab958.r2.cloudflarestorage.com/crate-test/",
		Region:    "auto",
		AccessKey: "test-access",
		SecretKey: "test-secret",
		Date:      mustDate(t, "2026-05-19T12:00:00Z"),
	})
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	if got := headers.Get("X-Amz-Content-Sha256"); got != emptyBodySHA256 {
		t.Errorf("payload hash = %q, want emptyBodySHA256", got)
	}
	auth := headers.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization missing algorithm: %q", auth)
	}
	if !strings.Contains(auth, "Credential=test-access/20260519/auto/s3/aws4_request") {
		t.Errorf("Authorization wrong scope: %q", auth)
	}
	// SignedHeaders should at minimum include host;x-amz-content-sha256;x-amz-date.
	if !strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization wrong signed-headers: %q", auth)
	}
}

// TestSignRequest_UnsignedPayload checks that UnsignedPayload is propagated
// to the payload hash header verbatim (so R2 accepts large streamed PUTs).
func TestSignRequest_UnsignedPayload(t *testing.T) {
	headers, err := SignRequest(SignOpts{
		Method:      http.MethodPut,
		URL:         "https://62231b040ed00c96cdcf3a4541eab958.r2.cloudflarestorage.com/crate-test/big-file.bin",
		Region:      "auto",
		AccessKey:   "test-access",
		SecretKey:   "test-secret",
		PayloadHash: UnsignedPayload,
		Date:        mustDate(t, "2026-05-19T12:00:00Z"),
	})
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	if got := headers.Get("X-Amz-Content-Sha256"); got != UnsignedPayload {
		t.Errorf("payload hash = %q, want UNSIGNED-PAYLOAD", got)
	}
}

// TestSignRequest_RejectsMissingFields verifies the input-validation surface.
func TestSignRequest_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		opts SignOpts
	}{
		{"no URL", SignOpts{Region: "auto", AccessKey: "a", SecretKey: "b"}},
		{"no region", SignOpts{URL: "https://x.example/", AccessKey: "a", SecretKey: "b"}},
		{"no access key", SignOpts{URL: "https://x.example/", Region: "auto", SecretKey: "b"}},
		{"no secret key", SignOpts{URL: "https://x.example/", Region: "auto", AccessKey: "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SignRequest(c.opts); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

// TestCanonicalPath checks RFC-3986 unreserved-character preservation +
// percent-encoding everything else.
func TestCanonicalPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/foo/bar", "/foo/bar"},
		{"/with space", "/with%20space"},
		{"/special!", "/special%21"},
		{"/unreserved-._~/", "/unreserved-._~/"},
		{"/utf8/héllo", "/utf8/h%C3%A9llo"},
	}
	for _, c := range cases {
		if got := canonicalPath(c.in); got != c.want {
			t.Errorf("canonicalPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEndpointBuilders checks the URL shapes match the browser port's
// crate/lib/bucket.js endpoints.* family.
func TestEndpointBuilders(t *testing.T) {
	if got := EndpointR2("62231b040ed00c96cdcf3a4541eab958", "crate-test"); got !=
		"https://62231b040ed00c96cdcf3a4541eab958.r2.cloudflarestorage.com/crate-test/" {
		t.Errorf("EndpointR2: %s", got)
	}
	if got := EndpointHetzner("nbg1", "my-bucket"); got !=
		"https://my-bucket.nbg1.your-objectstorage.com/" {
		t.Errorf("EndpointHetzner: %s", got)
	}
	if got := EndpointB2("us-west-002", "my-bucket"); got !=
		"https://my-bucket.s3.us-west-002.backblazeb2.com/" {
		t.Errorf("EndpointB2: %s", got)
	}
	if got := EndpointAWS("us-east-1", "my-bucket"); got !=
		"https://my-bucket.s3.us-east-1.amazonaws.com/" {
		t.Errorf("EndpointAWS: %s", got)
	}

	// EndpointForProvider dispatches.
	url, ok := EndpointForProvider("r2", "acct", "auto", "bk")
	if !ok || url != "https://acct.r2.cloudflarestorage.com/bk/" {
		t.Errorf("dispatch R2: ok=%v url=%s", ok, url)
	}
	if _, ok := EndpointForProvider("unknown", "", "", ""); ok {
		t.Errorf("unknown provider should return ok=false")
	}
}

// --- keys.go tests ---------------------------------------------------------

// TestDeriveBucketCredsKey verifies determinism (same input → same key) and
// that different macaroon roots produce different keys.
func TestDeriveBucketCredsKey(t *testing.T) {
	root1 := bytes.Repeat([]byte{0xa1}, 32)
	root2 := bytes.Repeat([]byte{0xa2}, 32)

	k1a, err := DeriveBucketCredsKey(root1)
	if err != nil {
		t.Fatalf("derive 1a: %v", err)
	}
	k1b, err := DeriveBucketCredsKey(root1)
	if err != nil {
		t.Fatalf("derive 1b: %v", err)
	}
	k2, err := DeriveBucketCredsKey(root2)
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	if !bytes.Equal(k1a, k1b) {
		t.Errorf("HKDF not deterministic: %x vs %x", k1a, k1b)
	}
	if bytes.Equal(k1a, k2) {
		t.Errorf("different macaroon roots yielded same key")
	}
	if len(k1a) != crypto.KeySize {
		t.Errorf("key length = %d, want %d", len(k1a), crypto.KeySize)
	}

	// Wrong size is rejected.
	if _, err := DeriveBucketCredsKey([]byte{1, 2, 3}); err == nil {
		t.Errorf("short root should error")
	}
}

// TestSealOpenRoundTrip covers the happy path: seal then open returns the
// original plaintext.
func TestSealOpenRoundTrip(t *testing.T) {
	root := bytes.Repeat([]byte{0xab}, 32)
	key, err := DeriveBucketCredsKey(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	secret := []byte("super-secret-r2-key-bytes")
	ct, nonce, err := SealSecret(key, secret, "bk_01HXY...")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if len(nonce) != crypto.NonceSize {
		t.Errorf("nonce length = %d, want %d", len(nonce), crypto.NonceSize)
	}
	if bytes.Equal(ct, secret) {
		t.Errorf("ciphertext equals plaintext — not encrypted")
	}
	got, err := OpenSecret(key, ct, nonce, "bk_01HXY...")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("round-trip mismatch: %q vs %q", got, secret)
	}
}

// TestSealOpen_AADBinding verifies that opening with the wrong bucket_id
// (row-swap attack) fails.
func TestSealOpen_AADBinding(t *testing.T) {
	root := bytes.Repeat([]byte{0xcd}, 32)
	key, err := DeriveBucketCredsKey(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	ct, nonce, err := SealSecret(key, []byte("alpha"), "bk_A")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := OpenSecret(key, ct, nonce, "bk_B"); err == nil {
		t.Errorf("OpenSecret with wrong bucket_id (AAD swap) should fail")
	}
}

// TestSealSecret_EmptyAndAAD checks the empty-plaintext + empty-AAD guards.
func TestSealSecret_EmptyAndAAD(t *testing.T) {
	root := bytes.Repeat([]byte{0xef}, 32)
	key, err := DeriveBucketCredsKey(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if _, _, err := SealSecret(key, nil, "bk"); err == nil {
		t.Errorf("seal empty should fail")
	}
	if _, _, err := SealSecret(key, []byte("x"), ""); err == nil {
		t.Errorf("seal with empty AAD should fail")
	}
	if _, err := OpenSecret(key, []byte{1}, make([]byte, crypto.NonceSize), ""); err == nil {
		t.Errorf("open with empty AAD should fail")
	}
}
