package nasaimages_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/nasaimages"
)

func TestNASAImages_Conformance(t *testing.T) {
	a := nasaimages.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	// The mars and images APIs live on different hosts; mock both with a
	// single mux that dispatches by path prefix.
	mux := http.NewServeMux()
	mux.HandleFunc("/mars-photos/api/v1/rovers/curiosity/photos", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"photos": []map[string]any{{"id": 1}}})
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": map[string]any{"items": []any{}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	plan := bridge.Plan{
		WantName:       "nasa-images",
		WantVersion:    "1.0.0",
		MinOperations:  2,
		FixtureHandler: mux.ServeHTTP,
		InjectBaseURL: func(_ *bridge.PlanCall, base string) {
			// Both upstreams collapse to the single test server.
			a.WithBaseURLs(base, base)
		},
		Calls: []bridge.PlanCall{
			{
				Operation:   "mars-photos",
				Params:      map[string]any{"rover": "curiosity", "sol": float64(1000)},
				Credentials: map[string]string{"api_key": "test"},
			},
			{
				Operation: "search-images",
				Params:    map[string]any{"q": "moon"},
			},
		},
	}
	bridge.RunConformance(t, a, plan)
}
