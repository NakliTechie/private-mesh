package webhookpost_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/webhookpost"
)

func TestWebhookPost_Conformance(t *testing.T) {
	a := webhookpost.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	// Records what the adapter sent so we can assert the idempotency header.
	var lastIdempotency string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastIdempotency = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		body, _ := io.ReadAll(r.Body)
		_ = body
		_ = json.NewEncoder(w).Encode(map[string]any{"received": true})
	}))
	t.Cleanup(srv.Close)

	plan := bridge.Plan{
		WantName:       "webhook-post",
		WantVersion:    "1.0.0",
		MinOperations:  1,
		FixtureHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"received": true})
		}),
		InjectBaseURL: func(call *bridge.PlanCall, base string) {
			call.Params["url"] = base + "/hook"
		},
		Calls: []bridge.PlanCall{
			{Operation: "post", Params: map[string]any{
				"body":    map[string]any{"alert": "fired"},
				"headers": map[string]any{"X-Tool": "stance"},
			}},
		},
	}
	bridge.RunConformance(t, a, plan)

	// Extra assertion: idempotency header surfaces.
	_, err := a.Call(t.Context(), &bridge.CallRequest{
		Operation: "post",
		Params: map[string]any{
			"url":  srv.URL + "/hook",
			"body": map[string]any{"alert": "fired again"},
		},
		IdempotencyKey: "idem-12345",
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if lastIdempotency != "idem-12345" {
		t.Errorf("Idempotency-Key not forwarded: got %q", lastIdempotency)
	}
}
