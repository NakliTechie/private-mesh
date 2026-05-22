package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

// hostAwareStub is a Bridge adapter that lets the test drive
// EffectiveHost via a Params["url"] entry. It mirrors the shape of
// webhookpost without pulling in its full HTTP plumbing — the new
// Hub-side enforcement only depends on the EffectiveHost interface,
// not on what the adapter does after the caveat check.
type hostAwareStub struct{}

func (hostAwareStub) Name() string    { return "host-aware-stub" }
func (hostAwareStub) Version() string { return "1.0.0" }
func (hostAwareStub) Operations() []bridge.OperationSpec {
	return []bridge.OperationSpec{{Name: "call", SideEffects: true}}
}

// EffectiveHost makes us a bridge.AdapterEffectiveHost. Returns the
// host portion of params["url"] so the Hub's caveat enforcer can match
// against it instead of the caller-supplied req.Domain.
func (hostAwareStub) EffectiveHost(params map[string]any) (string, error) {
	s, ok := params["url"].(string)
	if !ok || s == "" {
		return "", fmt.Errorf("missing params.url")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bad url")
	}
	return u.Hostname(), nil
}

func (hostAwareStub) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	// Sentinel — if caveat enforcement allows the call through, we get
	// here and the test asserts the path was reached.
	return &bridge.CallResponse{Result: map[string]any{"reached": true}}, nil
}

// installHostAwareBridge wires the stub adapter into the fixture's
// bridge registry.
func (h *hubFixture) installHostAwareBridge(t *testing.T) {
	t.Helper()
	reg := bridge.NewRegistry(nil)
	reg.MustRegister(hostAwareStub{})
	h.srv.SetBridgeRegistry(reg)
}

// TestBridgeOnlyDomain_DerivedFromAdapterParams asserts the audit fix:
// the `only-domain in [allowed.example.com]` caveat is now evaluated
// against the adapter's EffectiveHost(params), NOT the caller-supplied
// req.Domain. Pre-fix, an attacker could spoof "domain": "allowed.example.com"
// while pointing params.url at any internal IP.
func TestBridgeOnlyDomain_DerivedFromAdapterParams(t *testing.T) {
	h := newHubFixture(t)
	h.installHostAwareBridge(t)
	g := h.mintGrantWithScope(t, "bridge", "*", []string{"call"}, []string{
		"only-domain in [allowed.example.com]",
		"idempotency-required",
	})

	// Attacker: claim "allowed.example.com" but point params.url at a
	// metadata IP. Pre-fix: 200; post-fix: 403 caveat_unmet.
	body, _ := json.Marshal(map[string]any{
		"adapter":   "host-aware-stub",
		"operation": "call",
		"domain":    "allowed.example.com",
		"params": map[string]any{
			"url": "http://169.254.169.254/latest/meta-data/",
		},
	})
	status, respBody := h.doRaw(t, "POST", "/fabric/v1/bridge/call",
		io.NopCloser(bytes.NewReader(body)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           g,
			"X-Fabric-Idempotency-Key": "test-bridge-attacker-domain",
		},
	)
	if status != http.StatusBadRequest && status != http.StatusForbidden {
		t.Fatalf("expected 400 or 403, got %d; body=%s", status, respBody)
	}
}

// TestBridgeOnlyDomain_DomainMatchesParamsHost — the legitimate path:
// caller-supplied domain matches the adapter's EffectiveHost, and the
// derived host satisfies the only-domain caveat.
func TestBridgeOnlyDomain_DomainMatchesParamsHost(t *testing.T) {
	h := newHubFixture(t)
	h.installHostAwareBridge(t)
	g := h.mintGrantWithScope(t, "bridge", "*", []string{"call"}, []string{
		"only-domain in [allowed.example.com]",
		"idempotency-required",
	})

	body, _ := json.Marshal(map[string]any{
		"adapter":   "host-aware-stub",
		"operation": "call",
		"domain":    "allowed.example.com",
		"params": map[string]any{
			"url": "https://allowed.example.com/hook",
		},
	})
	status, respBody := h.doRaw(t, "POST", "/fabric/v1/bridge/call",
		io.NopCloser(bytes.NewReader(body)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           g,
			"X-Fabric-Idempotency-Key": "test-bridge-legit-domain",
		},
	)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", status, respBody)
	}
}

// TestBridgeOnlyDomain_DomainOmittedUsesAdapterHost — when the caller
// doesn't send a `domain` field at all, the Hub still derives the
// effective host from params and enforces the caveat against it.
func TestBridgeOnlyDomain_DomainOmittedUsesAdapterHost(t *testing.T) {
	h := newHubFixture(t)
	h.installHostAwareBridge(t)
	g := h.mintGrantWithScope(t, "bridge", "*", []string{"call"}, []string{
		"only-domain in [allowed.example.com]",
		"idempotency-required",
	})

	body, _ := json.Marshal(map[string]any{
		"adapter":   "host-aware-stub",
		"operation": "call",
		// no domain field
		"params": map[string]any{
			"url": "https://allowed.example.com/hook",
		},
	})
	status, respBody := h.doRaw(t, "POST", "/fabric/v1/bridge/call",
		io.NopCloser(bytes.NewReader(body)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           g,
			"X-Fabric-Idempotency-Key": "test-bridge-no-domain",
		},
	)
	if status != http.StatusOK {
		t.Fatalf("expected 200 (effective host satisfies caveat), got %d; body=%s", status, respBody)
	}
}
