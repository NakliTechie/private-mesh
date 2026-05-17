package cloudflarer2_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/cloudflarer2"
)

func TestCloudflareR2_Conformance(t *testing.T) {
	a := cloudflarer2.New()
	_ = a.Init(bridge.AdapterInitOptions{})

	stored := map[string][]byte{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			http.Error(w, "missing SigV4 auth", http.StatusUnauthorized)
			return
		}
		// strip leading /
		path := strings.TrimPrefix(r.URL.Path, "/")
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			stored[path] = b
			w.Header().Set("ETag", `"deadbeef"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<?xml version="1.0"?><ListBucketResult><Name>` + strings.SplitN(path, "/", 2)[0] + `</Name><KeyCount>` +
					"0" + `</KeyCount></ListBucketResult>`))
				return
			}
			if b, ok := stored[path]; ok {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(b)
			} else {
				http.Error(w, "not found", http.StatusNotFound)
			}
		case http.MethodDelete:
			delete(stored, path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	plan := bridge.Plan{
		WantName:       "cloudflare-r2",
		WantVersion:    "1.0.0",
		MinOperations:  4,
		FixtureHandler: handler,
		InjectBaseURL: func(_ *bridge.PlanCall, base string) {
			a.WithEndpointURL(base)
		},
		Calls: []bridge.PlanCall{
			{
				Operation: "put-object",
				Params: map[string]any{
					"bucket":   "test-bucket",
					"key":      "hello.txt",
					"body_b64": base64.StdEncoding.EncodeToString([]byte("hello world")),
				},
				Credentials: testCreds(),
			},
			{
				Operation: "get-object",
				Params: map[string]any{
					"bucket": "test-bucket",
					"key":    "hello.txt",
				},
				Credentials: testCreds(),
			},
			{
				Operation: "list-objects",
				Params: map[string]any{
					"bucket": "test-bucket",
				},
				Credentials: testCreds(),
			},
			{
				Operation: "delete-object",
				Params: map[string]any{
					"bucket": "test-bucket",
					"key":    "hello.txt",
				},
				Credentials: testCreds(),
			},
		},
	}
	bridge.RunConformance(t, a, plan)
}

func TestCloudflareR2_MissingCredential(t *testing.T) {
	a := cloudflarer2.New()
	_ = a.Init(bridge.AdapterInitOptions{})
	_, err := a.Call(t.Context(), &bridge.CallRequest{
		Operation: "get-object",
		Params:    map[string]any{"bucket": "x", "key": "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "access_key_id") {
		t.Errorf("expected missing-credential error for access_key_id, got %v", err)
	}
}

func testCreds() map[string]string {
	return map[string]string{
		"access_key_id":     "TESTACCESSKEY",
		"secret_access_key": "TESTSECRETKEYTESTSECRETKEYTESTSECRETKEY",
		"account_id":        "test-account",
	}
}
