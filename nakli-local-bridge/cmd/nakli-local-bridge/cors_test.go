package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOriginAllowed: the allowlist matcher's contract.
func TestOriginAllowed(t *testing.T) {
	allowed := []string{"https://crate.naklios.dev", "https://naklios.dev"}

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"crate.naklios.dev exact match", "https://crate.naklios.dev", true},
		{"naklios.dev exact match", "https://naklios.dev", true},
		{"localhost any port (dev)", "http://localhost:5173", true},
		{"127.0.0.1 any port (dev)", "http://127.0.0.1:8000", true},
		{"localhost https", "https://localhost:8443", true},
		{"random attacker site", "https://evil.example.com", false},
		{"crate.naklios.dev wrong scheme", "http://crate.naklios.dev", false},
		{"subdomain not allowed", "https://api.naklios.dev", false},
		{"empty rejects", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := originAllowed(tc.origin, allowed); got != tc.want {
				t.Errorf("origin=%q: got %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

// TestWithCORSRespectsAllowlist asserts the CORS middleware echoes the
// request's Origin header only for allowlisted origins. Pre-fix it set
// `*` unconditionally.
func TestWithCORSRespectsAllowlist(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withCORS(inner, []string{"https://crate.naklios.dev"})

	cases := []struct {
		name           string
		origin         string
		wantACAOEqual  string // "" → header should be absent
	}{
		{"allowlisted origin echoed", "https://crate.naklios.dev", "https://crate.naklios.dev"},
		{"localhost echoed (loopback rule)", "http://localhost:5173", "http://localhost:5173"},
		{"random origin gets no ACAO header", "https://evil.example.com", ""},
		{"no Origin header → no ACAO (curl-style)", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/local/peers", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			got := rec.Header().Get("Access-Control-Allow-Origin")
			if got != tc.wantACAOEqual {
				t.Errorf("ACAO: got %q, want %q", got, tc.wantACAOEqual)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("status: got %d, want 200 (CORS gating doesn't block the handler — browser enforces)", rec.Code)
			}
		})
	}
}
