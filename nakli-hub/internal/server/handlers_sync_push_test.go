package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// syncPushEvent is the shape /sync/push expects per event.
type syncPushEvent struct {
	EventID             string `json:"event_id"`
	Namespace           string `json:"namespace"`
	StreamID            string `json:"stream_id"`
	StreamType          string `json:"stream_type"`
	Kind                string `json:"kind"`
	PayloadCiphertext   string `json:"payload_ciphertext"`
	PreviousEventHash   string `json:"previous_event_hash,omitempty"`
	EventHash           string `json:"event_hash,omitempty"`
	AppendedByPrincipal string `json:"appended_by_principal"`
	AppendedByGrantID   string `json:"appended_by_grant_id"`
	AppendedAt          string `json:"appended_at,omitempty"`
}

func pushEvents(t *testing.T, h *hubFixture, gr string, events []syncPushEvent) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		t.Fatal(err)
	}
	return h.doRaw(t, "POST", "/fabric/v1/sync/push",
		io.NopCloser(bytes.NewReader(body)),
		map[string]string{
			"Content-Type":   "application/json",
			"X-Fabric-Grant": gr,
		},
	)
}

// TestSyncPush_RejectsForgedHistoryHash covers the security regression
// for forged history-chain attribution: pre-fix, a peer could push an
// event with any event_hash they liked and the receiver stored it
// verbatim. Now the receiver recomputes from
// (previous_event_hash || event_id || kind || metadata || causal_deps)
// and rejects mismatches.
func TestSyncPush_RejectsForgedHistoryHash(t *testing.T) {
	h := newHubFixture(t)
	gr := h.mintGrantWithScope(t, "sync", "*", []string{"push"}, nil)

	ev := syncPushEvent{
		EventID:           "01JEV0FORGEDHASH00000000001",
		Namespace:         storage.HistoryNamespace,
		StreamID:          "01JSTREAMSYNCPUSH0000000001",
		StreamType:        "history",
		Kind:              "test/event",
		PayloadCiphertext: base64.StdEncoding.EncodeToString([]byte("ct")),
		// Attacker-supplied hash that does NOT match the recomputed value.
		EventHash:           base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xFF}, 32)),
		AppendedByPrincipal: "01JATTACKER000000000000001",
		AppendedByGrantID:   "01JGRANT000000000000000001",
		AppendedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	status, body := pushEvents(t, h, gr, []syncPushEvent{ev})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	if err := jsonUnmarshalTest(body, &env); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Accepted int      `json:"accepted"`
		Skipped  int      `json:"skipped"`
		Errors   []string `json:"errors"`
	}
	if err := jsonUnmarshalTest(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Accepted != 0 {
		t.Errorf("forged-hash event was accepted (%d); should be 0", data.Accepted)
	}
	if data.Skipped != 1 {
		t.Errorf("skipped: got %d, want 1", data.Skipped)
	}
	if len(data.Errors) == 0 {
		t.Fatal("expected an error message naming the rejected event")
	}
	if !bytes.Contains([]byte(data.Errors[0]), []byte("event_hash mismatch")) {
		t.Errorf("error did not mention event_hash mismatch: %q", data.Errors[0])
	}
}

// TestSyncPush_HistoryHashOmittedIsAccepted covers the legitimate case
// where the sender doesn't supply an event_hash — the receiver fills it
// in from the recomputation. This preserves federation flows that
// don't pre-compute hashes on the sender side.
func TestSyncPush_HistoryHashOmittedIsAccepted(t *testing.T) {
	h := newHubFixture(t)
	gr := h.mintGrantWithScope(t, "sync", "*", []string{"push"}, nil)

	ev := syncPushEvent{
		EventID:             "01JEV0SYNCHASHOK00000000001",
		Namespace:           storage.HistoryNamespace,
		StreamID:            "01JSTREAMSYNCPUSH0000000002",
		StreamType:          "history",
		Kind:                "test/event",
		PayloadCiphertext:   base64.StdEncoding.EncodeToString([]byte("ct")),
		AppendedByPrincipal: "01JAUTHOR000000000000000001",
		AppendedByGrantID:   "01JGRANT000000000000000002",
		AppendedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	status, body := pushEvents(t, h, gr, []syncPushEvent{ev})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = jsonUnmarshalTest(body, &env)
	var data struct {
		Accepted int `json:"accepted"`
	}
	_ = jsonUnmarshalTest(env.Data, &data)
	if data.Accepted != 1 {
		t.Errorf("legitimate event was not accepted; body=%s", body)
	}
}

// TestSyncPush_StrictAttributionRejectsForgedPrincipal covers the
// attribution flag: when on, the receiver requires the event's claimed
// appended_by_principal to match the sender's grant principal. Default
// off preserves federation; this test flips it.
func TestSyncPush_StrictAttributionRejectsForgedPrincipal(t *testing.T) {
	h := newHubFixture(t, func(c *config.Config) {
		c.Auth.StrictSyncPushAttribution = true
	})
	gr, senderPrincipal := mintSyncPushGrantAndPrincipal(t, h)

	ev := syncPushEvent{
		EventID:             "01JEVATTRFORGE000000000001",
		Namespace:           "ns",
		StreamID:            "01JSTREAMSYNCPUSH0000000003",
		StreamType:          "vault",
		Kind:                "test/event",
		PayloadCiphertext:   base64.StdEncoding.EncodeToString([]byte("ct")),
		AppendedByPrincipal: "01JNOTTHESENDER0000000000001", // intentionally != sender
		AppendedByGrantID:   "01JGRANT000000000000000003",
		AppendedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	status, body := pushEvents(t, h, gr, []syncPushEvent{ev})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = jsonUnmarshalTest(body, &env)
	var data struct {
		Accepted int      `json:"accepted"`
		Skipped  int      `json:"skipped"`
		Errors   []string `json:"errors"`
	}
	_ = jsonUnmarshalTest(env.Data, &data)
	if data.Accepted != 0 {
		t.Errorf("forged-attribution event was accepted; should be 0")
	}
	if data.Skipped != 1 {
		t.Errorf("skipped: got %d, want 1", data.Skipped)
	}
	_ = senderPrincipal // keeps the helper signature meaningful

	// Sanity: same event with the correct attribution is accepted.
	ev.EventID = "01JEVATTROK000000000000001"
	ev.AppendedByPrincipal = senderPrincipal
	status, body = pushEvents(t, h, gr, []syncPushEvent{ev})
	if status != http.StatusOK {
		t.Fatalf("legitimate-attribution status: got %d, want 200; body=%s", status, body)
	}
	_ = jsonUnmarshalTest(body, &env)
	_ = jsonUnmarshalTest(env.Data, &data)
	if data.Accepted != 1 {
		t.Errorf("legitimate-attribution event not accepted; body=%s", body)
	}
}

// TestSyncPush_DefaultAttributionPreservesFederation asserts the flag
// defaults to OFF — multi-author federation flows (Hub A presenting
// Hub A's principals' events to Hub B) keep working without explicit
// operator opt-out.
func TestSyncPush_DefaultAttributionPreservesFederation(t *testing.T) {
	h := newHubFixture(t) // default cfg → StrictSyncPushAttribution false
	gr := h.mintGrantWithScope(t, "sync", "*", []string{"push"}, nil)

	ev := syncPushEvent{
		EventID:             "01JEVDEFATTR00000000000001",
		Namespace:           "ns",
		StreamID:            "01JSTREAMSYNCPUSH0000000004",
		StreamType:          "vault",
		Kind:                "test/event",
		PayloadCiphertext:   base64.StdEncoding.EncodeToString([]byte("ct")),
		AppendedByPrincipal: "01JANYOTHERPRINCIPAL000001", // != sender's grant principal
		AppendedByGrantID:   "01JGRANT000000000000000004",
		AppendedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	status, body := pushEvents(t, h, gr, []syncPushEvent{ev})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = jsonUnmarshalTest(body, &env)
	var data struct {
		Accepted int `json:"accepted"`
	}
	_ = jsonUnmarshalTest(env.Data, &data)
	if data.Accepted != 1 {
		t.Errorf("default mode rejected a cross-principal event (the test guarding federation compat); body=%s", body)
	}
}

// mintSyncPushGrantAndPrincipal mints a sync:push grant tied to a known
// principal id so the strict-attribution test can assert exact equality.
func mintSyncPushGrantAndPrincipal(t *testing.T, h *hubFixture) (gr string, principal string) {
	t.Helper()
	principal = "01JSYNCPUSHTESTSENDER000001"
	gr = h.mintGrantWithScopeAs(t, principal, grant.Primitive("sync"), "*", []string{"push"}, nil)
	// Touch the context import so an unused-import error doesn't appear
	// in editors when this file is opened standalone.
	_ = context.Background()
	return gr, principal
}
