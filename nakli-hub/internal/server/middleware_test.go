package server_test

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// TestIdempotencyMiddlewareRejectsOversizedBody covers the body-size cap
// added to defend against memory-exhaustion DoS via the idempotency
// middleware (previously did io.ReadAll with no MaxBytesReader). A grant-
// holder posting a multi-GB body could OOM the Hub; the middleware now
// caps reads at 2x MaxEventSizeBytes + 256 KiB header slop.
func TestIdempotencyMiddlewareRejectsOversizedBody(t *testing.T) {
	h := newHubFixture(t)
	mac := h.mintGrant(t)

	// Default MaxEventSizeBytes is 1 MiB; cap = 2 MiB + 256 KiB ≈ 2.25 MiB.
	// 16 MiB is comfortably over the cap.
	oversized := bytes.Repeat([]byte{'x'}, 16<<20)

	status, body := h.doRaw(t, "POST", "/fabric/v1/vault/append",
		io.NopCloser(bytes.NewReader(oversized)),
		map[string]string{
			"Content-Type":              "application/json",
			"X-Fabric-Grant":            mac,
			"X-Fabric-Idempotency-Key":  "test-oversized-body-key",
		},
	)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want %d; body=%s", status, http.StatusRequestEntityTooLarge, body)
	}
}

// TestIdempotencyMiddlewareAcceptsNormalBody covers the regression
// counterpart: well-sized requests still go through. Uses an obviously-
// invalid JSON body so we just check we reached the handler (which
// rejects with bad_request, NOT request entity too large).
func TestIdempotencyMiddlewareAcceptsNormalBody(t *testing.T) {
	h := newHubFixture(t)
	mac := h.mintGrant(t)

	body := []byte(`{"not":"a real vault append, but small"}`)
	status, _ := h.doRaw(t, "POST", "/fabric/v1/vault/append",
		io.NopCloser(bytes.NewReader(body)),
		map[string]string{
			"Content-Type":              "application/json",
			"X-Fabric-Grant":            mac,
			"X-Fabric-Idempotency-Key":  "test-normal-body-key",
		},
	)
	if status == http.StatusRequestEntityTooLarge {
		t.Fatalf("normal-sized body was incorrectly rejected as too large; status=%d", status)
	}
}

// TestVaultAppendChecksAuthBeforeBlobWrite covers the fix that moved
// checkAuth above WriteBlob. Earlier ordering wrote ciphertext to disk
// at blobs/<aa>/<bb>/<event_id>.bin BEFORE checking the grant's scope.
// Any holder of a scope-mismatched grant could fill the disk by
// looping write attempts to a denied namespace — the orphan blob
// would persist with no DB row pointing at it.
func TestVaultAppendChecksAuthBeforeBlobWrite(t *testing.T) {
	h := newHubFixture(t)
	mac := mintGrantForNamespace(t, h, "allowed-ns")

	payloadCiphertext := base64.StdEncoding.EncodeToString([]byte("would-be-blob-bytes"))
	reqBody, _ := json.Marshal(map[string]any{
		"namespace": "denied-ns",
		"stream_id": "01JTESTSTREAM0000000001",
		"event": map[string]any{
			"kind":               "test/event",
			"payload_ciphertext": payloadCiphertext,
		},
	})
	status, body := h.doRaw(t, "POST", "/fabric/v1/vault/append",
		io.NopCloser(bytes.NewReader(reqBody)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           mac,
			"X-Fabric-Idempotency-Key": "test-auth-before-blob-key",
		},
	)
	if status != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", status, body)
	}

	// Walk the blobs directory — should be empty (no orphan blob from the
	// rejected request).
	root := h.store.BlobsRoot()
	var stray []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".bin") {
			stray = append(stray, path)
		}
		return nil
	})
	if len(stray) != 0 {
		t.Fatalf("scope-denied request left %d orphan blob(s): %v", len(stray), stray)
	}
}

// mintGrantForNamespace mints a vault grant restricted to a single
// namespace. Use it to construct deliberately-scope-mismatched test
// requests.
func mintGrantForNamespace(t *testing.T, h *hubFixture, namespace string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	pid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	g, err := grant.Mint(grant.MintSpec{
		RootKey:  h.id.MacaroonRootKey,
		Location: h.ts.URL,
		Identifier: grant.Identifier{
			GrantID:           gid.String(),
			IssuedAt:          now,
			IssuedByPrincipal: pid.String(),
			IssuedByKeypair:   pub,
			Scope: grant.Scope{
				Primitive:  grant.PrimitiveVault,
				Namespace:  namespace,
				Operations: []string{"read", "write"},
			},
		},
		Caveats: []string{"operation in [read, write]"},
	})
	if err != nil {
		t.Fatalf("grant.Mint: %v", err)
	}
	return base64.StdEncoding.EncodeToString(g.Macaroon)
}
