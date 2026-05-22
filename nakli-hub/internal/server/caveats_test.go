package server_test

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/server"
)

// TestCaveatBindingStrictMode covers the security-audit fix for the bypass
// where `agent-id == <id>`, `device-id == <id>`, and `principal-type in [...]`
// caveats were silently satisfied when the caller omitted the corresponding
// X-Fabric-* header. With cfg.Auth.StrictCaveatBinding=true the missing
// header IS the failure.
func TestCaveatBindingStrictMode(t *testing.T) {
	cases := []struct {
		name           string
		caveat         string
		matchValue     string // value present in the caveat (after ==)
		setRequester   func(ctx *server.CaveatContext, v string)
		errSubstrMatch string // substring of the expected error message on strict miss
	}{
		{
			name:       "agent-id ==",
			caveat:     "agent-id == 01JAGENTSAMPLE0000000001",
			matchValue: "01JAGENTSAMPLE0000000001",
			setRequester: func(ctx *server.CaveatContext, v string) {
				ctx.RequesterAgentID = v
			},
			errSubstrMatch: "X-Fabric-Agent-Id",
		},
		{
			name:       "device-id ==",
			caveat:     "device-id == 01JDEVICESAMPLE000000001",
			matchValue: "01JDEVICESAMPLE000000001",
			setRequester: func(ctx *server.CaveatContext, v string) {
				ctx.RequesterDeviceID = v
			},
			errSubstrMatch: "X-Fabric-Device-Id",
		},
		{
			name:       "principal-type in [human]",
			caveat:     "principal-type in [human]",
			matchValue: "human",
			setRequester: func(ctx *server.CaveatContext, v string) {
				ctx.RequesterPrincipalType = v
			},
			errSubstrMatch: "X-Fabric-Principal-Type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Lax mode + header absent → pass (preserves current behavior).
			ctx := server.CaveatContext{Now: time.Now()}
			if err := server.EvaluateCaveats([]string{tc.caveat}, ctx); err != nil {
				t.Errorf("lax mode + absent header: unexpected error %v (this would break existing consumers)", err)
			}

			// Lax mode + correct header → pass.
			ctx = server.CaveatContext{Now: time.Now()}
			tc.setRequester(&ctx, tc.matchValue)
			if err := server.EvaluateCaveats([]string{tc.caveat}, ctx); err != nil {
				t.Errorf("lax mode + matching header: unexpected error %v", err)
			}

			// Lax mode + mismatched header → fail (existing behavior).
			ctx = server.CaveatContext{Now: time.Now()}
			tc.setRequester(&ctx, "wrong-value")
			if err := server.EvaluateCaveats([]string{tc.caveat}, ctx); err == nil {
				t.Errorf("lax mode + mismatched header: expected caveat-unmet error, got nil")
			}

			// Strict mode + header absent → FAIL (the bypass fix).
			ctx = server.CaveatContext{Now: time.Now(), StrictBinding: true}
			err := server.EvaluateCaveats([]string{tc.caveat}, ctx)
			if err == nil {
				t.Errorf("strict mode + absent header: expected caveat-unmet error, got nil — the bypass is back")
			}
			var ce *server.CaveatError
			if !errors.As(err, &ce) {
				t.Errorf("strict mode + absent header: error is not *CaveatError: %T", err)
			} else if !strings.Contains(ce.Reason, tc.errSubstrMatch) {
				t.Errorf("strict mode + absent header: error reason %q does not mention %q", ce.Reason, tc.errSubstrMatch)
			}

			// Strict mode + correct header → pass.
			ctx = server.CaveatContext{Now: time.Now(), StrictBinding: true}
			tc.setRequester(&ctx, tc.matchValue)
			if err := server.EvaluateCaveats([]string{tc.caveat}, ctx); err != nil {
				t.Errorf("strict mode + matching header: unexpected error %v", err)
			}

			// Strict mode + mismatched header → fail.
			ctx = server.CaveatContext{Now: time.Now(), StrictBinding: true}
			tc.setRequester(&ctx, "wrong-value")
			if err := server.EvaluateCaveats([]string{tc.caveat}, ctx); err == nil {
				t.Errorf("strict mode + mismatched header: expected caveat-unmet error, got nil")
			}
		})
	}
}

// TestCaveatBindingDefaultFlag asserts that the new flag's default (false)
// preserves prior behavior — i.e., this PR does NOT silently break existing
// consumers that don't yet send the binding headers.
func TestCaveatBindingDefaultFlag(t *testing.T) {
	// A zero-valued CaveatContext (StrictBinding=false) MUST accept a caveat
	// without the corresponding header — that's the documented backward-
	// compatible behavior the flag preserves.
	for _, caveat := range []string{
		"agent-id == 01JAGENTSAMPLE0000000001",
		"device-id == 01JDEVICESAMPLE000000001",
		"principal-type in [human]",
	} {
		ctx := server.CaveatContext{Now: time.Now()}
		if err := server.EvaluateCaveats([]string{caveat}, ctx); err != nil {
			t.Errorf("default flag + absent header for %q: got %v, want nil (must preserve compat)", caveat, err)
		}
	}
}

// TestCaveatBindingFlagWiredToHTTP is the integration test: it proves the
// Auth.StrictCaveatBinding config knob actually reaches the live caveat
// evaluator on an authenticated HTTP request. With strict ON and a
// device-id-bound grant, a request that omits X-Fabric-Device-Id MUST be
// rejected with 403 caveat_unmet.
func TestCaveatBindingFlagWiredToHTTP(t *testing.T) {
	const boundDeviceID = "01JDEVICETESTSTRICT000001"

	h := newHubFixture(t, func(c *config.Config) {
		c.Auth.StrictCaveatBinding = true
	})
	mac := mintGrantWithBindingCaveats(t, h, []string{
		"device-id == " + boundDeviceID,
		"operation in [read, write]",
	})

	payloadCiphertext := base64.StdEncoding.EncodeToString([]byte("strict-mode-test"))
	reqBody, _ := json.Marshal(map[string]any{
		"namespace": "allowed-ns",
		"stream_id": "01JTESTSTREAM0000000001",
		"event": map[string]any{
			"kind":               "test/event",
			"payload_ciphertext": payloadCiphertext,
		},
	})

	// Without the binding header → strict mode must reject the caveat.
	status, body := h.doRaw(t, "POST", "/fabric/v1/vault/append",
		io.NopCloser(bytes.NewReader(reqBody)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           mac,
			"X-Fabric-Idempotency-Key": "test-strict-no-header",
		},
	)
	if status != http.StatusForbidden {
		t.Fatalf("strict + no header: got %d, want 403; body=%s", status, body)
	}
	var env errorEnv
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "caveat_unmet" {
		t.Errorf("strict + no header: code=%q, want caveat_unmet", env.Error.Code)
	}

	// With the correct binding header → request passes auth (the caveat is
	// satisfied; the handler may still fail later for unrelated reasons, but
	// NOT with caveat_unmet).
	status, body = h.doRaw(t, "POST", "/fabric/v1/vault/append",
		io.NopCloser(bytes.NewReader(reqBody)),
		map[string]string{
			"Content-Type":             "application/json",
			"X-Fabric-Grant":           mac,
			"X-Fabric-Device-Id":       boundDeviceID,
			"X-Fabric-Idempotency-Key": "test-strict-with-header",
		},
	)
	if status == http.StatusForbidden {
		_ = json.Unmarshal(body, &env)
		if env.Error.Code == "caveat_unmet" {
			t.Errorf("strict + matching header: caveat_unmet returned anyway; body=%s", body)
		}
	}
}

// mintGrantWithBindingCaveats produces a grant whose caveat list is exactly
// the supplied set. Use it to exercise the strict-binding flag end-to-end.
func mintGrantWithBindingCaveats(t *testing.T, h *hubFixture, caveats []string) string {
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
				Namespace:  "allowed-ns",
				Operations: []string{"read", "write"},
			},
		},
		Caveats: caveats,
	})
	if err != nil {
		t.Fatalf("grant.Mint: %v", err)
	}
	return base64.StdEncoding.EncodeToString(g.Macaroon)
}
