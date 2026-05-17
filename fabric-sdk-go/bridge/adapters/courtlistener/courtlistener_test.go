package courtlistener_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/courtlistener"
)

func TestCourtListener_Conformance(t *testing.T) {
	a := courtlistener.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	fixture := bridge.NewFixtureServer().
		Handle("/api/rest/v3/search/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("q") == "" {
				http.Error(w, "missing q", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count":   1,
				"results": []map[string]any{{"id": 42, "case_name": "fourth amendment test"}},
			})
		}).
		Handle("/api/rest/v3/opinions/42/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "type": "opinion"})
		}).
		Handle("/api/rest/v3/dockets/77/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "type": "docket"})
		})

	plan := bridge.Plan{
		WantName:       "courtlistener",
		WantVersion:    "1.0.0",
		MinOperations:  3,
		FixtureHandler: fixture.ServeHTTP,
		InjectBaseURL: func(call *bridge.PlanCall, base string) {
			a.WithBaseURL(base)
		},
		Calls: []bridge.PlanCall{
			{Operation: "search", Params: map[string]any{"q": "fourth amendment"}},
			{Operation: "get-opinion", Params: map[string]any{"id": float64(42)}},
			{Operation: "get-docket", Params: map[string]any{"id": float64(77)}},
		},
	}
	bridge.RunConformance(t, a, plan)
}

func TestCourtListener_MissingQueryParam(t *testing.T) {
	a := courtlistener.New()
	_ = a.Init(bridge.AdapterInitOptions{})
	_, err := a.Call(t.Context(), &bridge.CallRequest{Operation: "search", Params: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "q") {
		t.Errorf("expected missing-param error mentioning q, got %v", err)
	}
}
