# Authoring a Bridge adapter

A Bridge adapter is a small Go module that translates `{operation, params, credentials}` into one or more HTTP requests to an external service, and the response back into a structured result. Spec: [`docs/specs/bridge-adapters-spec-001-v1.1.md`](../../../docs/specs/bridge-adapters-spec-001-v1.1.md).

In M5.5, the v1.0 starter catalogue (eight adapters) lives in this directory. Out-of-tree adapters are a v1.x project.

This guide walks through adding a new adapter. The existing adapters in this directory are the best reference — copy the closest match and modify.

## 1. Decide the shape

Before writing code, answer:

- **Service** — what API are you wrapping?
- **Operations** — kebab-case names matching the spec, one per logical action (e.g. `search`, `put-object`).
- **Credentials** — the canonical credential keys (`api_key`, `access_key_id` + `secret_access_key`, etc.). Stored in the user's FIF, passed per-call in `CallRequest.Credentials`.
- **Side effects** — operations that mutate external state must set `SideEffects=true` on the `OperationSpec`. Side-effectful operations require the Grant to carry `idempotency-required`.
- **Estimable** — true if a dry-run can return cost/duration without executing.
- **Required caveats** — adapters that touch money or external mailers should document in their README which Grant caveats are essentially required for safe agent use (e.g., `email-resend send` recommends `requires-human-approval` for unbounded use).

## 2. Skeleton

```go
package myadapter

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
    adapterName    = "my-adapter"
    adapterVersion = "1.0.0"
    defaultBaseURL = "https://api.example.com"
)

type Adapter struct {
    client  *http.Client
    baseURL string
}

func New() *Adapter                              { return &Adapter{baseURL: defaultBaseURL} }
func (a *Adapter) WithBaseURL(u string) *Adapter { a.baseURL = u; return a }

func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
    if opts.HTTPClient != nil { a.client = opts.HTTPClient }
    if a.client == nil { a.client = &http.Client{Timeout: 15 * time.Second} }
    return nil
}
func (a *Adapter) Close() error    { return nil }
func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
    return []bridge.OperationSpec{
        // declare one OperationSpec per operation
    }
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
    // 1. Dispatch on req.Operation FIRST; return ErrUnknownOperation for
    //    anything outside the declared catalogue. This must precede any
    //    credential / param lookup so the conformance runner's
    //    unknown-operation check passes.
    switch req.Operation {
    case "my-op":
        return a.myOp(ctx, req)
    default:
        return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
    }
}
```

## 3. Implement one operation

A typical operation:

```go
func (a *Adapter) myOp(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
    apiKey, err := bridge.ResolveCredential(req.Credentials, "api_key")
    if err != nil { return nil, err }
    q, err := bridge.ResolveParam[string](req.Params, "q", true, "")
    if err != nil { return nil, err }

    start := time.Now()
    r, _ := http.NewRequestWithContext(ctx, http.MethodGet,
        a.baseURL+"/v1/search?q="+url.QueryEscape(q), nil)
    r.Header.Set("Authorization", "Bearer "+apiKey)
    resp, err := a.client.Do(r)
    if err != nil {
        return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, err)
    }
    defer resp.Body.Close()
    raw, _ := io.ReadAll(resp.Body)
    if resp.StatusCode >= 400 {
        return nil, fmt.Errorf("%w: upstream returned %d", bridge.ErrUpstreamUnavailable, resp.StatusCode)
    }
    var data map[string]any
    _ = json.Unmarshal(raw, &data)
    return &bridge.CallResponse{
        Result: data,
        Metrics: bridge.CallMetrics{
            DurationMs: int(time.Since(start).Milliseconds()) + 1,
            BytesIn:    len(raw),
        },
    }, nil
}
```

Rules:
- **Honor `ctx`** — `http.NewRequestWithContext`, not `http.NewRequest`. The conformance runner's cancellation test depends on this.
- **`+1` on DurationMs** — `time.Since` rounds to zero on fast mocked calls, and the conformance runner asserts `DurationMs > 0`.
- **Map errors to the catalogue** — `ErrMissingParam`, `ErrInvalidParam`, `ErrMissingCredential`, `ErrUpstreamUnavailable`, `ErrUnknownOperation`.
- **Never persist credentials** — store nothing on the adapter struct that came from `req.Credentials`. The Hub's caveat layer is the security perimeter, not the adapter.

## 4. Write the test

Every adapter ships with `<name>_test.go` and uses `bridge.RunConformance` to validate the eight conformance properties.

```go
func TestMyAdapter_Conformance(t *testing.T) {
    a := myadapter.New()
    _ = a.Init(bridge.AdapterInitOptions{})

    fixture := bridge.NewFixtureServer().
        Handle("/v1/search", func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            _ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
        })

    plan := bridge.Plan{
        WantName:       "my-adapter",
        WantVersion:    "1.0.0",
        MinOperations:  1,
        FixtureHandler: fixture.ServeHTTP,
        InjectBaseURL: func(_ *bridge.PlanCall, base string) {
            a.WithBaseURL(base)
        },
        Calls: []bridge.PlanCall{
            {
                Operation:   "my-op",
                Params:      map[string]any{"q": "x"},
                Credentials: map[string]string{"api_key": "test"},
            },
        },
    }
    bridge.RunConformance(t, a, plan)
}
```

The runner validates: interface basics, happy-path per operation, unknown-operation rejection, cancellation, and that the adapter doesn't mutate `req.Credentials`.

## 5. Register

Add the adapter to `nakli-hub/cmd/nakli-hub/main.go`'s `newBridgeRegistry()` so it's available at `GET /fabric/v1/bridge/adapters`:

```go
import "github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge/adapters/myadapter"

r.MustRegister(myadapter.New())
```

## 6. README

Each adapter directory MUST contain a short README covering:

1. What service it wraps + a link to the upstream API docs
2. The credential keys it expects (`api_key`, `access_key_id` + …)
3. Per-operation table (params, return shape, side-effects, idempotency notes)
4. **Recommended Grant caveats** — especially for side-effectful adapters. Example: "`email-resend send` recommends `requires-human-approval` for unbounded use or `only-domain in [trusted.example]` for known recipients."
5. Cost notes when applicable

## 7. Submit

For v1.0, open a PR against `fabric-sdk-go`. NakliTechie owns the review bar. v1.x may open this with a more formal process and out-of-tree distribution under `@naklitechie-adapters/*`.

## Reference adapters

| Adapter | What it shows |
| --- | --- |
| [`courtlistener`](courtlistener/) | Simplest possible: 3 GET operations, optional API key, no encryption |
| [`archive-org`](archiveorg/) | Anonymous reads against multiple URL prefixes |
| [`nasa-images`](nasaimages/) | Two upstream hosts (mars + images), optional API key |
| [`webhook-post`](webhookpost/) | Generic POST with caller-supplied headers + body |
| [`email-resend`](emailresend/) | Bearer auth, native Idempotency-Key header, side-effect operation |
| [`anthropic-claude`](anthropicclaude/) | Non-bearer header auth (`x-api-key` + `anthropic-version`) |
| [`openai-compatible`](openaicompatible/) | Per-call base URL override (so one adapter serves OpenRouter, Together, vLLM, …) |
| [`cloudflare-r2`](cloudflarer2/) | AWS SigV4 signing (inline implementation), four S3-compatible operations |

## Security checklist

Before merging:
- [ ] No credential persists beyond the call lifetime
- [ ] `ctx` is honored in every HTTP call
- [ ] Unknown operations return `ErrUnknownOperation` BEFORE credential resolution
- [ ] Errors map to the catalogue (`bridge.Err*`)
- [ ] The adapter is stateless beyond the HTTP client + base URL
- [ ] No fabric state is mutated (no Vault writes, no FIF reads)
- [ ] README documents recommended Grant caveats for safe agent use
