package emailresend_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/emailresend"
)

func TestEmailResend_Conformance(t *testing.T) {
	a := emailresend.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	fixture := bridge.NewFixtureServer().Handle("/emails", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if parsed["subject"] == nil {
			http.Error(w, "missing subject", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "re-12345"})
	})

	plan := bridge.Plan{
		WantName:       "email-resend",
		WantVersion:    "1.0.0",
		MinOperations:  1,
		FixtureHandler: fixture.ServeHTTP,
		InjectBaseURL:  func(_ *bridge.PlanCall, base string) { a.WithBaseURL(base) },
		Calls: []bridge.PlanCall{
			{
				Operation: "send",
				Params: map[string]any{
					"from":    "ops@nakli.test",
					"to":      []any{"bhai@example.com"},
					"subject": "M5.5 gate",
					"text":    "hi",
				},
				Credentials: map[string]string{"api_key": "test-key"},
			},
		},
	}
	bridge.RunConformance(t, a, plan)
}

func TestEmailResend_MissingAPIKey(t *testing.T) {
	a := emailresend.New()
	_ = a.Init(bridge.AdapterInitOptions{})
	_, err := a.Call(t.Context(), &bridge.CallRequest{
		Operation: "send",
		Params: map[string]any{
			"from": "x@y", "to": []any{"a@b"}, "subject": "hi",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("expected missing-credential error mentioning api_key, got %v", err)
	}
}
