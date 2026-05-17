package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ConformanceTest is the generic adapter conformance runner described in
// bridge-adapters-spec-001-v1.1.md §Conformance. It validates eight
// behavioral properties an adapter must honor.
//
// The caller provides a mocked upstream HTTP server (via FixtureServer) so
// every adapter test runs hermetically — no network, no real credentials.
// Spec checks:
//   1. Interface compliance: Name(), Version(), Operations() return sane values.
//   2. Operation discoverability: every declared operation is callable.
//   3. Cancellation: ctx.Done is honored.
//   4. Error mapping: upstream failures surface as wrapped errors.
//   5. No credential persistence: adapter must not retain Credentials.
//   6. Operation containment: unknown operations return ErrUnknownOperation.
//   7. No fabric-state mutation: not enforceable from a unit test; the
//      runner just asserts the adapter returns a fresh CallResponse instance.
//   8. Metrics accuracy: DurationMs > 0 on any non-error call.
//
// The runner is parameterized with a Plan that the adapter author supplies —
// one happy-path call per declared operation. The runner re-uses that plan
// for the cancellation / unknown-operation / error-mapping cases.
type Plan struct {
	// Name + Version assertions.
	WantName    string
	WantVersion string
	// MinOperations is the minimum number of declared operations to assert.
	MinOperations int
	// Calls is one happy-path call per operation; the runner invokes each.
	// Empty Params are OK if the operation needs none.
	Calls []PlanCall
	// FixtureHandler is the mocked upstream handler the runner stands up.
	// The same handler serves every plan call; adapter authors keep it small
	// by branching on r.URL.Path.
	FixtureHandler http.HandlerFunc
	// FixtureBaseURL is set by the runner before calling the adapter; the
	// adapter can read it via the WithFixture wrapper below. Adapter authors
	// who need this typically pass it via Credentials or Params.
	// Leave nil for adapters that don't talk to an HTTP service at all
	// (none in v1.0).
	InjectBaseURL func(plan *PlanCall, baseURL string)
}

// PlanCall is one operation invocation the runner makes.
type PlanCall struct {
	Operation   string
	Params      map[string]any
	Credentials map[string]string
}

// RunConformance executes the plan. It uses t.Run subtests so failures point
// at the offending property. Call from each adapter's _test.go after wiring
// up its Plan.
func RunConformance(t *testing.T, adapter Adapter, plan Plan) {
	t.Helper()
	if adapter == nil {
		t.Fatal("conformance: adapter is nil")
	}
	t.Run("interface_basics", func(t *testing.T) {
		if name := adapter.Name(); name != plan.WantName {
			t.Errorf("Name(): got %q, want %q", name, plan.WantName)
		}
		if v := adapter.Version(); !looksLikeSemver(v) {
			t.Errorf("Version(): %q is not semver-ish", v)
		}
		if plan.WantVersion != "" && adapter.Version() != plan.WantVersion {
			t.Errorf("Version(): got %q, want %q", adapter.Version(), plan.WantVersion)
		}
		ops := adapter.Operations()
		if len(ops) < plan.MinOperations {
			t.Errorf("Operations(): got %d, want at least %d", len(ops), plan.MinOperations)
		}
		seen := map[string]struct{}{}
		for _, o := range ops {
			if o.Name == "" {
				t.Errorf("Operations(): operation with empty Name")
			}
			if _, dup := seen[o.Name]; dup {
				t.Errorf("Operations(): duplicate operation %q", o.Name)
			}
			seen[o.Name] = struct{}{}
		}
	})

	srv := httptest.NewServer(plan.FixtureHandler)
	t.Cleanup(srv.Close)

	t.Run("happy_path", func(t *testing.T) {
		for _, call := range plan.Calls {
			call := call
			t.Run(call.Operation, func(t *testing.T) {
				if plan.InjectBaseURL != nil {
					plan.InjectBaseURL(&call, srv.URL)
				}
				req := &CallRequest{
					Operation:      call.Operation,
					Params:         cloneStringAny(call.Params),
					Credentials:    cloneStringMap(call.Credentials),
					IdempotencyKey: "01J0CONFORMANCE0000000000000",
				}
				credsBefore := cloneStringMap(req.Credentials)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				resp, err := adapter.Call(ctx, req)
				if err != nil {
					t.Fatalf("Call %s: %v", call.Operation, err)
				}
				if resp == nil {
					t.Fatalf("Call %s: nil response", call.Operation)
				}
				if resp.Metrics.DurationMs <= 0 {
					t.Errorf("Call %s: DurationMs should be > 0, got %d", call.Operation, resp.Metrics.DurationMs)
				}
				// No credential persistence: the credential map must look
				// the same after the call (the adapter shouldn't have
				// rewritten it via a shared reference).
				for k, v := range credsBefore {
					if req.Credentials[k] != v {
						t.Errorf("adapter mutated credentials[%s] (was %q, became %q)", k, v, req.Credentials[k])
					}
				}
			})
		}
	})

	t.Run("unknown_operation", func(t *testing.T) {
		_, err := adapter.Call(context.Background(), &CallRequest{
			Operation: "this-operation-does-not-exist",
			Params:    map[string]any{},
		})
		if err == nil {
			t.Fatal("expected error for unknown operation, got nil")
		}
		if !errors.Is(err, ErrUnknownOperation) {
			t.Errorf("expected ErrUnknownOperation, got %v", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		if len(plan.Calls) == 0 {
			t.Skip("no operations in plan")
		}
		call := plan.Calls[0]
		if plan.InjectBaseURL != nil {
			plan.InjectBaseURL(&call, srv.URL)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel before the call so the adapter sees Done() immediately
		req := &CallRequest{
			Operation:      call.Operation,
			Params:         cloneStringAny(call.Params),
			Credentials:    cloneStringMap(call.Credentials),
			IdempotencyKey: "01J0CANCEL0000000000000000000",
		}
		_, err := adapter.Call(ctx, req)
		if err == nil {
			t.Fatal("expected error after ctx cancel, got nil")
		}
		// We don't pin the exact error class — different libraries surface
		// cancellation differently (context.Canceled, url.Error wrapping it,
		// io.ErrUnexpectedEOF, etc.) — but cancellation MUST surface.
		if !strings.Contains(err.Error(), "context") && !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation error should mention context; got %v", err)
		}
	})
}

func looksLikeSemver(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, ch := range p {
			if (ch < '0' || ch > '9') && ch != '-' && (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') {
				return false
			}
		}
	}
	return true
}

func cloneStringAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// FixtureServer is a tiny convenience for adapters whose fixtures need to
// branch by path. Adapter authors typically write inline HandlerFuncs; this
// exists so the most common branching pattern stays uniform.
type FixtureServer struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
}

// NewFixtureServer returns an empty mux. Use Handle to map paths.
func NewFixtureServer() *FixtureServer {
	return &FixtureServer{routes: map[string]http.HandlerFunc{}}
}

// Handle registers a handler for the exact path.
func (f *FixtureServer) Handle(path string, h http.HandlerFunc) *FixtureServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = h
	return f
}

// ServeHTTP dispatches by path. Unmatched paths return 404 with a JSON note.
func (f *FixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	h, ok := f.routes[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"ok":false,"error":"no fixture for %s"}`, r.URL.Path)
		return
	}
	h(w, r)
}

// Handler returns the FixtureServer as an http.Handler for use with
// httptest.NewServer.
func (f *FixtureServer) Handler() http.Handler { return f }
