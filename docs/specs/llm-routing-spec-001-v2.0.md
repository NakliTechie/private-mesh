# LLM Primitive Routing Specification

**Document:** `llm-routing-spec-001-v2.0.md`
**Status:** v2.0 draft (Phase 2; v1.0 ships basic remote-BYOK only)
**Companion to:** `fabric-spec-001-v1.0.md`, `fabric-sdk-go-spec-001-v1.0.md`, `fabric-sdk-js-spec-001-v1.0.md`
**Audience:** Implementers of the LLM primitive's routing layer; consumers calling `llm.complete`.

The LLM primitive (per D11) sits over MLX, llama.cpp, browser-local inference (Transformers.js / wllama), and remote BYOK providers. The protocol endpoint (`POST /fabric/v1/llm/complete`) is in `fabric-spec-001-v1.0.md`. This spec defines:
- The route catalogue and how routes are discovered, ranked, and selected
- The runtime implementations (anchor LLM server, browser backends, remote adapters)
- The cost/latency surface
- Caching, deduplication, and failure handling specific to LLM operations

Phase 1 ships only remote BYOK. Phase 2 adds anchor-local (MLX/llama.cpp) and browser-local routes. This document specifies the full v2.0 shape.

---

## Scope

This document specifies:
- Route catalogue: types, capabilities, lifecycle
- Routing algorithm: how a `complete` request maps to a route
- Anchor LLM server (`nakli-llm-server`): MLX + llama.cpp wrapper
- Browser-local backends: integration points for Transformers.js and wllama
- Remote BYOK adapters: Anthropic, OpenAI, Groq, Mistral, local proxies
- Cost accounting and surface
- Capability negotiation (model-aware routing)
- Streaming responses
- Failure handling and route fallback

Out of scope:
- Specific model selection within a route (consumer's responsibility via `model` field)
- Training, fine-tuning, or evaluation (separate concerns; Tapasya handles those)
- Embedding/vector operations (separate primitive; deferred to v2.x)
- Audio/image generation (deferred; v2.x or beyond)

---

## Conceptual model

A **route** is a path from `POST /fabric/v1/llm/complete` to bytes-of-completion. Each route has:
- A **type**: `anchor-local`, `browser-local`, `remote-byok`, `peer-routed`
- A set of **capabilities**: context window, vision support, function-calling support, structured output support
- A **cost model**: free, per-token, per-request, flat
- A **latency profile**: first-token-ms, tokens-per-second, p99
- An **availability state**: online, offline, degraded

The routing layer maintains a catalogue of known routes, ranks them per request, and dispatches.

---

## Route catalogue

### `anchor-local` routes

The user's anchor (M4 Max Studio, Strix Halo, DGX Spark, etc.) runs `nakli-llm-server` exposing local models.

- **MLX backend** — Apple Silicon: MLX-Swift or MLX-Python serves models with native Metal acceleration
- **llama.cpp backend** — cross-platform: GGUF models via llama.cpp's HTTP server or embedded library
- **vLLM / Ollama / LM Studio adapters** — if the user already runs one of these, anchor-local routes can wrap them

Route discovery: `nakli-llm-server` advertises via mDNS (`_nakli-llm._tcp.local`) and registers with the local Hub. The Hub's `/fabric/v1/llm/routes` endpoint returns the catalogue.

Capabilities are reported per loaded model:
```json
{
  "route_id": "anchor-local:mlx:llama-3-70b",
  "type": "anchor-local",
  "backend": "mlx",
  "model": "llama-3-70b-instruct-4bit",
  "capabilities": {
    "context_window": 32000,
    "vision": false,
    "function_calling": true,
    "structured_output": true
  },
  "latency_profile": {
    "first_token_ms": 180,
    "tokens_per_second": 22,
    "warm": true
  },
  "cost": { "type": "free" },
  "availability": "online"
}
```

### `browser-local` routes

Browser-side inference via Transformers.js (WebGPU/WASM) or wllama (GGUF in browser).

- Tools register a browser backend with the SDK at runtime: `fabric.llm.registerBrowserBackend(...)`
- Models load on first use; subsequent requests are warm
- Cost: free (user's CPU/GPU)
- Latency: highly variable; small models work, large models don't fit
- Capabilities: depend on the model; SDK does not invent capabilities

The SDK does NOT bundle models. The tool provides them (via CDN, OPFS cache, etc.) and registers the backend. This keeps the SDK small and makes the model choice explicit.

### `remote-byok` routes

External providers reached with the user's API key. The credential lives in the FIF's `bridge_credentials`; this is reused by the LLM primitive.

Supported in v1.0:
- **Anthropic** — Claude models via Messages API
- **OpenAI** — GPT models via Chat Completions
- **Groq** — Fast inference via Groq's OpenAI-compatible API
- **Mistral** — Mistral models direct
- **OpenAI-compatible** — generic adapter for any compatible endpoint (OpenRouter, local proxies, etc.)

Each adapter is a small (~200 lines) module that translates the `complete` request shape to the provider's API and the response back. Adapters live in `fabric-sdk-go/llm/adapters/` and `fabric-sdk-js/llm/adapters/`.

### `peer-routed` routes

A peer in the user's mesh exposes their anchor's LLM server as a route the consumer can use. Per the network plane: peer-to-peer over NetBird mesh.

- Useful for: "my partner's M4 Max has Llama-3 70B; my MacBook doesn't; route to theirs when on the home mesh"
- Authorization: macaroon Grant on the LLM primitive from the peer's principal
- Cost: free (or accounted internally; not externalized)

Phase 2 adds this; Phase 1 does not.

---

## The routing algorithm

When a consumer calls `llm.complete(spec)`:

1. **Resolve eligible routes** from the catalogue:
   - Filter by `capabilities` — every route must meet `spec.capabilities` (min context, vision, function-calling)
   - Filter by `availability` — routes marked offline are skipped
   - Filter by Grant scope — Grant must permit this route's type (`only-route` caveat, see Caveats)
   - Filter by cost — `max_cost_cents` caveat must allow this route's projected cost

2. **Rank eligible routes** by:
   - `preferred_route` from spec (if set): exact match wins
   - Cost: lower wins (free > per-token > per-request)
   - Latency: lower wins
   - Locality: anchor-local > browser-local > peer-routed > remote-byok (the default sovereignty order)

3. **Attempt the top route**:
   - Send the request
   - If success: return result
   - If retryable error (unavailable, rate-limited): mark route degraded, try next
   - If non-retryable error (auth, content-policy, scope_denied): surface to consumer; do not fall back
   - If timeout: try next route

4. **All routes exhausted**:
   - Return `unavailable` error
   - Queue the operation per Hook 1 if `spec.queue_on_failure: true`

Important nuances:
- **Fallback only on transient failures**, never on content/policy/auth failures
- **Cost-bounded routing**: if a Grant has `max-amount` caveat and remote-byok is the only available route exceeding the budget, return `caveat_unmet` rather than silently downgrading
- **Capability-bounded routing**: if `needs_vision: true` and no available route supports vision, return `unavailable` with reason "no route supports vision"

---

## LLM-specific caveats

The macaroon caveat catalogue (in `fabric-spec-001-v1.0.md`) is extended with LLM-specific caveats:

- `only-route in [anchor-local, browser-local, peer-routed, remote-byok]` — restricts allowed route types
- `only-model in [model-id, ...]` — restricts allowed models (e.g., "this agent can use Llama-3 8B locally but not Claude")
- `max-tokens-per-request <= N` — limits per-request token usage
- `max-tokens-per-window <= N per <window>` — rate-limit by tokens, not by requests
- `denied-content-types in [vision, audio]` — block certain modalities even if route supports them

These caveats compose with the standard catalogue. Example: an agent Grant scoped to `llm:invoke` with `only-route=anchor-local`, `max-tokens-per-request<=10000`, `max-tokens-per-window<=1000000 per day` — local inference only, bounded cost (in compute time, not money), bounded daily volume.

---

## Anchor LLM server (`nakli-llm-server`)

Phase 2 component. Single binary (Go), bundles or shells out to MLX-Python and llama.cpp.

### Architecture

```
┌─────────────────────────────────────────┐
│  nakli-llm-server                       │
│                                         │
│  HTTP server (localhost:7844)           │
│  ├── /llm/complete  → adapter dispatch  │
│  ├── /llm/routes    → discovered models │
│  └── /llm/load      → load a model      │
│                                         │
│  Backend adapters:                      │
│  ├── MLX adapter (calls mlx-server)     │
│  ├── llama.cpp adapter (embedded/forked)│
│  └── Ollama/LM Studio adapter (HTTP)    │
└─────────────────────────────────────────┘
            │
            ▼ registered with local Hub via
   POST /fabric/v1/llm/register-route
```

### Configuration

```toml
# /etc/nakli-llm-server/config.toml

[server]
listen = "127.0.0.1:7844"
hub_url = "http://127.0.0.1:7842"   # registers with this Hub
hub_grant_path = "/etc/nakli-llm-server/hub-grant.macaroon"

[[backend]]
type = "mlx"
mlx_server_path = "/opt/mlx-server/mlx-server"   # or "embedded"
models_dir = "/var/lib/nakli-llm-server/mlx-models"

[[backend]]
type = "llama-cpp"
binary_path = "/usr/local/bin/llama-server"
models_dir = "/var/lib/nakli-llm-server/gguf-models"

[[backend]]
type = "ollama"
url = "http://127.0.0.1:11434"
```

### Model loading

Models are loaded explicitly:
```bash
nakli-llm-server load --model llama-3-70b-instruct-4bit --backend mlx
```

Or warm-loaded at startup via `auto_load = ["llama-3-70b-instruct-4bit", ...]`.

### Discovery and registration

On startup:
1. Each backend scans its models directory
2. Each available model is registered as a route with the Hub via `POST /fabric/v1/llm/register-route`
3. Routes update when models are loaded/unloaded
4. On graceful shutdown: routes deregister

### Streaming

Anchor-local routes stream tokens by default. Protocol surface:
- Request body: `{ stream: true }` → response is SSE stream of token deltas
- Request body: `{ stream: false }` (default) → response is a single completion

Streaming respects the same idempotency key — replaying a streaming request returns the same final completion (the server reconstructs from cache; partial replays are not supported in v2.0).

---

## Browser-local backends

The JS SDK exposes a registration API:

```typescript
fabric.llm.registerBrowserBackend({
  id: "transformers-js:phi-3-mini",
  model: "Xenova/Phi-3-mini-4k-instruct",
  capabilities: {
    context_window: 4000,
    vision: false,
    function_calling: false,
  },
  generate: async (messages, opts) => {
    // tool's implementation using @xenova/transformers
    const result = await pipeline(...)
    return { content, tokens };
  },
});
```

Multiple backends can be registered; each becomes a route.

### Lifecycle
- Backend registers at tool load
- First inference call loads the model (slow; surfaces as `availability: "loading"`)
- After load: model stays in memory until tab close or explicit `unregisterBrowserBackend`
- On out-of-memory: backend marks itself `availability: "broken"` until reload

### Storage
- Tools cache model weights in OPFS for offline use
- SDK does not manage model storage; tool decides

---

## Remote BYOK adapters

Each adapter is a thin translation layer.

### Adapter interface (Go)

```go
package adapters

type Adapter interface {
    Name() string  // e.g. "anthropic"
    Capabilities(model string) (*Capabilities, error)
    Complete(ctx context.Context, req *CompleteRequest, credential string) (*CompleteResponse, error)
    EstimateCost(req *CompleteRequest, model string) (cents int, ok bool)
}

type CompleteRequest struct {
    Model      string
    Messages   []Message
    MaxTokens  int
    Stream     bool
    Tools      []ToolDef  // for function-calling
    // ...
}

type CompleteResponse struct {
    Content      string
    Stop         string
    TokensIn     int
    TokensOut    int
    CostCents    int
    Stream       <-chan StreamChunk  // nil if !Stream
}
```

### Anthropic adapter (sketch)

```go
func (a *AnthropicAdapter) Complete(ctx, req, cred) (*CompleteResponse, error) {
    body := map[string]any{
        "model": req.Model,
        "messages": translateMessages(req.Messages),
        "max_tokens": req.MaxTokens,
    }
    if len(req.Tools) > 0 {
        body["tools"] = translateTools(req.Tools)
    }
    bodyJson, _ := json.Marshal(body)
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyJson))
    httpReq.Header.Set("x-api-key", cred)
    httpReq.Header.Set("anthropic-version", "2023-06-01")
    httpReq.Header.Set("content-type", "application/json")

    resp, err := a.client.Do(httpReq)
    if err != nil { return nil, mapTransportError(err) }
    defer resp.Body.Close()
    // ... parse response, handle errors, accounting
}
```

Adapters MUST:
- Translate provider errors to the fabric error catalogue (rate-limited, unavailable, etc.)
- Accurately estimate cost based on the provider's pricing (table per model embedded in adapter)
- Honor `ctx` cancellation
- Stream when requested (or return error if provider doesn't support streaming)

### Catalogue (v2.0 set)

| Adapter | Models | Streaming | Function calling | Vision |
|---|---|---|---|---|
| anthropic | claude-*-sonnet, claude-*-haiku, claude-*-opus | yes | yes | yes |
| openai | gpt-4*, gpt-5* (when out) | yes | yes | yes |
| groq | llama-*, mixtral-* | yes | partial | no |
| mistral | mistral-* | yes | yes | no |
| openai-compatible | any | configurable | configurable | configurable |

The `openai-compatible` adapter is generic; the user provides base URL and model name. Covers OpenRouter, Together AI, vLLM deployments, LM Studio's HTTP server.

---

## Cost accounting

Every completed request emits a cost record to the LLM operation log (separate from History; lives in the Hub's `operation_log` table or equivalent on Worker).

```json
{
  "request_id": "<ulid>",
  "grant_id": "<ulid>",
  "principal": "<ulid>",
  "route_id": "remote-byok:anthropic:claude-3-5-sonnet",
  "tokens_in": 1234,
  "tokens_out": 567,
  "cost_cents": 12,
  "duration_ms": 4200,
  "timestamp": "<rfc3339>"
}
```

The Hub aggregates daily/monthly totals per Grant, per principal, per route. CLI: `nakli-cli llm cost --since 2026-01-01 --by principal`.

`max-amount` caveats are enforced per-Grant cumulatively. Once exceeded, further `llm:invoke` operations return `caveat_unmet`.

---

## Capability negotiation

The consumer declares required capabilities:
```json
{
  "messages": [...],
  "capabilities": {
    "min_context_window": 32000,
    "needs_vision": true,
    "needs_function_calling": true
  }
}
```

Routing filters routes by these requirements. If no route meets them: `unavailable` with reason "no available route satisfies capabilities."

Consumer can override with `preferred_route: "exact-route-id"` — if that route is available and Grant permits, it's used regardless of capability match (consumer is making an informed choice).

---

## Streaming protocol

SSE for HTTP transports:

```
data: {"type": "delta", "content": "Hello"}
data: {"type": "delta", "content": " world"}
data: {"type": "tool-use", "name": "search", "input": {...}}
data: {"type": "stop", "reason": "end_turn", "tokens_in": 12, "tokens_out": 4, "cost_cents": 1}
```

For WebRTC transports (Local Network browser-to-browser): same JSON frames over data channel.

For Cloudflare Worker: SSE supported but with edge-runtime constraints; consumers should expect slightly higher first-token latency.

The SDK's `llm.complete()` accepts a `stream: true` flag and returns an async iterable:
```typescript
const stream = await fabric.llm.complete({ messages: [...], stream: true });
for await (const chunk of stream) {
  if (chunk.type === "delta") process.stdout.write(chunk.content);
}
```

---

## Failure handling specific to LLM

### Content policy violations
Some providers refuse certain requests. Adapter returns `error.code = "content_policy"` — non-retryable, no fallback. Consumer decides how to surface.

### Token-limit hits
Provider truncates at max_tokens. Response includes `stop: "max_tokens"`. Consumer handles (continue, summarize, etc.).

### Provider timeout
Adapter cancels via `ctx`; returns `unavailable`. Routing layer falls back to next route.

### Rate limiting
Provider returns 429. Adapter returns `rate_limited` with retry-after hint. Routing layer marks route degraded for the hint duration; falls back to next route.

### Streaming midway failure
If stream breaks mid-response: SDK emits a stream-error chunk; consumer decides whether to retry (with idempotency key — the duplicate detection by adapter prevents charging twice on retry).

---

## Phase 1 reduced scope

Phase 1 ships:
- `remote-byok` routes only
- Adapters: Anthropic, OpenAI, openai-compatible (three to start)
- No anchor-local, no browser-local, no peer-routed
- Streaming via SSE supported in adapters that support it
- Cost accounting in the Hub
- All caveat types accepted (even if routes are limited)

This makes Phase 1's LLM primitive useful immediately for users with API keys, without blocking on the anchor LLM server work.

Phase 2 adds anchor-local + browser-local + peer-routed.

---

## Out of scope

- Embedding generation (separate primitive, deferred to v2.x)
- Image/audio generation
- Fine-tuning operations (Tapasya's domain)
- Multi-LLM ensembling (consumer can do this on top)
- Caching/memoization beyond idempotency replay
- RAG retrieval (the LLM primitive does inference; retrieval is a separate concern, likely a tool-level concern)

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md` (LLM primitive endpoints)
- Decision D11 (LLM routing layer over MLX + llama.cpp)
- MLX: https://github.com/ml-explore/mlx
- llama.cpp: https://github.com/ggerganov/llama.cpp
- Transformers.js: https://github.com/xenova/transformers.js
- wllama: https://github.com/ngxson/wllama
