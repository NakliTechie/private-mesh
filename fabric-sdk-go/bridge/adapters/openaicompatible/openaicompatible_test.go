package openaicompatible_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/openaicompatible"
)

func TestOpenAICompatible_Conformance(t *testing.T) {
	a := openaicompatible.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	fixture := bridge.NewFixtureServer().
		Handle("/v1/chat/completions", echo(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})).
		Handle("/v1/completions", echo(map[string]any{"choices": []map[string]any{{"text": "ok"}}})).
		Handle("/v1/embeddings", echo(map[string]any{"data": []map[string]any{{"embedding": []float64{0.1, 0.2}}}}))

	plan := bridge.Plan{
		WantName:       "openai-compatible",
		WantVersion:    "1.0.0",
		MinOperations:  3,
		FixtureHandler: fixture.ServeHTTP,
		InjectBaseURL: func(call *bridge.PlanCall, base string) {
			call.Params["base_url"] = base
		},
		Calls: []bridge.PlanCall{
			{
				Operation: "chat-completions",
				Params: map[string]any{
					"model":    "gpt-4o-mini",
					"messages": []any{map[string]any{"role": "user", "content": "hi"}},
				},
				Credentials: map[string]string{"api_key": "test-key"},
			},
			{
				Operation: "completions",
				Params: map[string]any{
					"model":  "gpt-3.5-turbo-instruct",
					"prompt": "say hi",
				},
				Credentials: map[string]string{"api_key": "test-key"},
			},
			{
				Operation: "embeddings",
				Params: map[string]any{
					"model": "text-embedding-3-small",
					"input": []any{"hello"},
				},
				Credentials: map[string]string{"api_key": "test-key"},
			},
		},
	}
	bridge.RunConformance(t, a, plan)
}

func echo(payload map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}
}
