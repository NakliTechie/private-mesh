package archiveorg_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/archiveorg"
)

func TestArchiveOrg_Conformance(t *testing.T) {
	a := archiveorg.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	fixture := bridge.NewFixtureServer().
		Handle("/wayback/available", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("url") == "" {
				http.Error(w, "missing url", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"archived_snapshots": map[string]any{
					"closest": map[string]any{"status": "200", "available": true},
				},
			})
		}).
		Handle("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response": map[string]any{"numFound": 1, "docs": []map[string]any{{"identifier": "x"}}},
			})
		}).
		Handle("/metadata/test-item", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"title": "Test"}})
		})

	plan := bridge.Plan{
		WantName:       "archive-org",
		WantVersion:    "1.0.0",
		MinOperations:  3,
		FixtureHandler: fixture.ServeHTTP,
		InjectBaseURL:  func(_ *bridge.PlanCall, base string) { a.WithBaseURL(base) },
		Calls: []bridge.PlanCall{
			{Operation: "wayback-get", Params: map[string]any{"url": "https://example.com"}},
			{Operation: "search", Params: map[string]any{"q": "nakli"}},
			{Operation: "get-item", Params: map[string]any{"identifier": "test-item"}},
		},
	}
	bridge.RunConformance(t, a, plan)
}

func TestArchiveOrg_MissingURL(t *testing.T) {
	a := archiveorg.New()
	_ = a.Init(bridge.AdapterInitOptions{})
	_, err := a.Call(t.Context(), &bridge.CallRequest{Operation: "wayback-get", Params: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Errorf("expected missing-param error mentioning url, got %v", err)
	}
}
