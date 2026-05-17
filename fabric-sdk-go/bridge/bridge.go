// Package bridge defines the Bridge adapter interface and the v1.0 starter
// catalogue. Spec: docs/specs/bridge-adapters-spec-001-v1.1.md.
//
// An Adapter is a small, stateless module that translates a Bridge call
// ({adapter, operation, params}) into an HTTP request to an external service
// and the response back into a structured result. Adapters never persist
// credentials; the caller passes a Credentials map per call (sourced from the
// user's FIF).
//
// Registration: callers instantiate adapters they care about and Register
// them on a Registry. The Hub (and any transport) holds one Registry and
// dispatches incoming /bridge/call requests via Registry.Call.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Adapter is the protocol implementers fulfill. Every adapter advertises its
// name, its supported operations, and a Call method. Init/Close are optional
// lifecycle hooks.
type Adapter interface {
	// Name as referenced in Bridge calls' "adapter" field.
	Name() string
	// Operations this adapter supports.
	Operations() []OperationSpec
	// Version is semver (e.g. "1.0.0").
	Version() string
	// Call executes one operation. Adapters MUST honor ctx for cancellation.
	Call(ctx context.Context, req *CallRequest) (*CallResponse, error)
}

// AdapterWithLifecycle is an optional extension some adapters may implement
// to hold long-lived state (HTTP client pools, cached schema, etc.). The
// Registry will Init() on registration and Close() on shutdown.
type AdapterWithLifecycle interface {
	Adapter
	Init(opts AdapterInitOptions) error
	Close() error
}

// OperationSpec describes a single operation. Names are kebab-case (matching
// the spec's catalogue) so wire calls don't depend on programming-language
// naming conventions.
type OperationSpec struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Params      []ParamSpec `json:"params"`
	Returns     []ReturnSpec `json:"returns,omitempty"`
	// SideEffects = true requires the Grant to carry idempotency-required.
	SideEffects bool `json:"side_effects"`
	// Estimable = true means the adapter can return a cost/duration estimate
	// for this operation with DryRun=true.
	Estimable bool `json:"estimable"`
}

// ParamSpec is a JSON-schema-ish descriptor for input params.
type ParamSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`           // string | integer | boolean | object | array
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// ReturnSpec describes one field of the response payload.
type ReturnSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// AdapterInitOptions is the bag of init knobs an adapter may want — chiefly an
// HTTP client to reuse (so the Hub or tests can inject httptest.Client).
type AdapterInitOptions struct {
	HTTPClient *http.Client
}

// CallRequest is the per-call input. Adapters MUST treat Params + Credentials
// as read-only; they MUST NOT persist Credentials beyond the call.
type CallRequest struct {
	Operation      string
	Params         map[string]any
	Credentials    map[string]string
	IdempotencyKey string
	DryRun         bool
}

// CallResponse is the per-call output. Result is freely-shaped (the adapter
// decides); Metrics is structured.
type CallResponse struct {
	Result  map[string]any
	Metrics CallMetrics
}

// CallMetrics is a structured handful of post-call telemetry.
type CallMetrics struct {
	DurationMs    int        `json:"duration_ms"`
	BytesIn       int        `json:"bytes_in"`
	BytesOut      int        `json:"bytes_out"`
	CostCents     int        `json:"cost_cents,omitempty"`
	RateRemaining *RateState `json:"rate_remaining,omitempty"`
}

// RateState mirrors the external service's rate-limit headers when available.
type RateState struct {
	Remaining int `json:"remaining"`
	Limit     int `json:"limit"`
	ResetSec  int `json:"reset_sec"`
}

// Errors mapped to the protocol's error vocabulary.
var (
	ErrUnknownOperation   = errors.New("bridge: unknown operation")
	ErrMissingParam       = errors.New("bridge: missing required parameter")
	ErrInvalidParam       = errors.New("bridge: invalid parameter")
	ErrMissingCredential  = errors.New("bridge: missing credential")
	ErrUpstreamUnavailable = errors.New("bridge: upstream service unavailable")
	ErrAdapterNotFound    = errors.New("bridge: adapter not registered")
)

// Registry is the in-process catalogue used by the Hub (and any other
// transport) to dispatch /bridge/call to the right adapter.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	httpc    *http.Client
}

// NewRegistry returns an empty Registry. The optional HTTP client is the one
// adapters reuse; pass nil for the package default.
func NewRegistry(httpc *http.Client) *Registry {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &Registry{
		adapters: map[string]Adapter{},
		httpc:    httpc,
	}
}

// Register stores an adapter in the registry. Calling Register twice with the
// same Name() overwrites. If the adapter implements AdapterWithLifecycle,
// Init is called with the registry's shared HTTP client.
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return errors.New("bridge.Register: adapter is nil")
	}
	if a.Name() == "" {
		return errors.New("bridge.Register: adapter Name() is empty")
	}
	if l, ok := a.(AdapterWithLifecycle); ok {
		if err := l.Init(AdapterInitOptions{HTTPClient: r.httpc}); err != nil {
			return fmt.Errorf("bridge.Register: %s init: %w", a.Name(), err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Name()] = a
	return nil
}

// MustRegister panics on error. Intended for nakli-hub's main where the
// adapters are statically wired and failure is a programmer bug.
func (r *Registry) MustRegister(a Adapter) {
	if err := r.Register(a); err != nil {
		panic(err)
	}
}

// Get returns the adapter by name, or false.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// List returns the registered adapters sorted by name.
func (r *Registry) List() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// CatalogueEntry is the wire shape returned by GET /fabric/v1/bridge/adapters.
type CatalogueEntry struct {
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Operations []OperationSpec `json:"operations"`
	// Status is informational; M5.5 always reports "active". Future versions
	// may surface "deprecated" / "experimental".
	Status string `json:"status"`
}

// Catalogue serializes the registered adapters for the discovery endpoint.
func (r *Registry) Catalogue() []CatalogueEntry {
	out := []CatalogueEntry{}
	for _, a := range r.List() {
		out = append(out, CatalogueEntry{
			Name:       a.Name(),
			Version:    a.Version(),
			Operations: a.Operations(),
			Status:     "active",
		})
	}
	return out
}

// Call dispatches a Bridge call to the named adapter. Returns
// ErrAdapterNotFound if the adapter is not registered.
func (r *Registry) Call(ctx context.Context, adapterName string, req *CallRequest) (*CallResponse, error) {
	a, ok := r.Get(adapterName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, adapterName)
	}
	return a.Call(ctx, req)
}

// Close calls Close on every adapter that implements AdapterWithLifecycle.
// Errors are joined.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, a := range r.adapters {
		if l, ok := a.(AdapterWithLifecycle); ok {
			if err := l.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// HTTPClient returns the registry's shared client. Adapters that don't take
// an Init hook can fall back to this via the Registry.
func (r *Registry) HTTPClient() *http.Client { return r.httpc }

// ResolveParam is a small helper for adapters: pull a typed value out of
// req.Params with an optional default. Returns ErrMissingParam when required
// and absent.
func ResolveParam[T any](params map[string]any, key string, required bool, def T) (T, error) {
	v, ok := params[key]
	if !ok {
		if required {
			return def, fmt.Errorf("%w: %s", ErrMissingParam, key)
		}
		return def, nil
	}
	t, ok := v.(T)
	if !ok {
		return def, fmt.Errorf("%w: %s: got %T", ErrInvalidParam, key, v)
	}
	return t, nil
}

// ResolveCredential is the matching helper for required-credential lookup.
func ResolveCredential(creds map[string]string, key string) (string, error) {
	v, ok := creds[key]
	if !ok || v == "" {
		return "", fmt.Errorf("%w: %s", ErrMissingCredential, key)
	}
	return v, nil
}
