package server_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
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
