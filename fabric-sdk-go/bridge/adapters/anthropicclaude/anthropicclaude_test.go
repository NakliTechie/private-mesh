package anthropicclaude_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/anthropicclaude"
)

func TestAnthropicClaude_Conformance(t *testing.T) {
	a := anthropicclaude.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	fixture := bridge.NewFixtureServer().Handle("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("anthropic-version") == "" {
			http.Error(w, "missing version", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "msg_test",
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]any{"input_tokens": 5, "output_tokens": 2},
		})
	})

	plan := bridge.Plan{
		WantName:       "anthropic-claude",
		WantVersion:    "1.0.0",
		MinOperations:  1,
		FixtureHandler: fixture.ServeHTTP,
		InjectBaseURL:  func(_ *bridge.PlanCall, base string) { a.WithBaseURL(base) },
		Calls: []bridge.PlanCall{
			{
				Operation: "messages",
				Params: map[string]any{
					"model":      "claude-3-5-sonnet-latest",
					"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
					"max_tokens": float64(50),
				},
				Credentials: map[string]string{"api_key": "test-key"},
			},
		},
	}
	bridge.RunConformance(t, a, plan)
}
