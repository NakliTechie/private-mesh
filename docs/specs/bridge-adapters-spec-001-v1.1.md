# Bridge Adapter Catalogue and Authoring Specification

**Document:** `bridge-adapters-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `bridge-adapters-spec-001-v1.0.md` — adds explicit dependency choices per adapter (canonical SDKs where they exist) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md`
**Audience:** Implementers writing Bridge adapters; tool authors invoking Bridge calls; reviewers.

The Bridge primitive (in `fabric-spec-001-v1.0.md`) is the fabric's interface to the outside world. It accepts `{adapter, operation, params}` and translates that into a call to some external service, with the user's BYOK credential.

This spec defines:
- What an adapter is (the interface)
- How adapters are packaged, distributed, and installed
- The v1.0 starter catalogue
- The authoring guide for new adapters
- Security posture
- Versioning
- Conformance

**Critical positioning:** the fabric never bundles credentials or accounts. Adapters are pluggable; users opt in. NakliTechie does not pre-approve adapters; the user does, by installing them and granting credentials.

---

## Dependencies

Bridge adapters are intentionally small. Each adapter has its own dependency choice, listed per-adapter in the catalogue below. General principles:

- **Use the canonical SDK** for that service if it exists, is well-maintained, and adds genuine value. For example: AWS SDK for S3-compatible storage (cloudflare-r2), Anthropic SDK for Claude (anthropic-claude).
- **Use plain `net/http` / `fetch`** for adapters where the SDK is heavy or unnecessary. Most adapters in the v1.0 catalogue fall here.
- **Forbidden:** rolling our own HTTP client. Use stdlib.

The adapter interface (Go and JS) lives in the SDKs and has no external dependencies beyond stdlib + the SDK's own crypto/macaroon dependencies.

---

## What an adapter is

An adapter is a small module (Go for native consumers, TypeScript for browser consumers, both for portability) that:
- Declares a name (e.g., `"courtlistener"`)
- Declares operations it supports (e.g., `"search"`, `"get-opinion"`)
- Translates a Bridge call into an HTTP request to the external service
- Translates the response back into a structured result
- Reports cost/usage metrics
- Honors cancellation, timeouts, and rate limits

An adapter is NOT:
- A credential store (credentials live in the user's FIF; passed per-call)
- A scheduler (adapters are synchronous; scheduling is a tool concern)
- An aggregator across services (one adapter = one service, possibly with multiple operations)
- A state holder beyond its call lifetime (stateless beyond optional caching)

---

## Adapter interface

### Go interface

```go
package bridge

type Adapter interface {
    // Name as referenced in Bridge calls' "adapter" field.
    Name() string

    // Operations this adapter supports.
    Operations() []OperationSpec

    // Execute a Bridge operation.
    Call(ctx context.Context, req *CallRequest) (*CallResponse, error)

    // Adapter version (semver).
    Version() string

    // Optional: lifecycle hooks
    Init(opts AdapterInitOptions) error
    Close() error
}

type OperationSpec struct {
    Name        string
    Description string
    Params      []ParamSpec       // JSON-schema-ish descriptors
    Returns     []ReturnSpec
    SideEffects bool              // true → requires idempotency-required Grant caveat
    Estimable   bool              // true → adapter can estimate cost before calling
}

type CallRequest struct {
    Operation     string
    Params        map[string]any
    Credential    string          // BYOK; pulled from FIF
    IdempotencyKey string
    DryRun        bool
}

type CallResponse struct {
    Result   map[string]any
    Metrics  CallMetrics
}

type CallMetrics struct {
    DurationMs    int
    BytesIn       int
    BytesOut      int
    CostCents     int             // 0 if not applicable
    RateRemaining *RateState      // optional; for rate-limit-aware throttling
}
```

### TypeScript interface

```typescript
export interface Adapter {
  name: string;
  version: string;
  operations: OperationSpec[];
  call(req: CallRequest, signal?: AbortSignal): Promise<CallResponse>;
  init?(opts: AdapterInitOptions): Promise<void>;
  close?(): Promise<void>;
}
```

The two interfaces map one-to-one; an adapter authored once should be portable if HTTP semantics are similar.

---

## Packaging and distribution

### v1.0 model: in-tree

Adapters are part of the SDK repositories:
- Go: `fabric-sdk-go/bridge/adapters/<name>/`
- JS: `fabric-sdk-js/bridge/adapters/<name>.ts` (or directory)

To use an adapter, the consumer imports it and registers:

**Go:**
```go
import "github.com/naklitechie/fabric-sdk-go/bridge/adapters/courtlistener"

bridgeClient.RegisterAdapter(courtlistener.New())
```

**JS:**
```typescript
import { CourtListener } from "@naklitechie/fabric-sdk/bridge/adapters/courtlistener";

fabric.bridge.registerAdapter(new CourtListener());
```

The Hub statically imports all adapters at build time (decided by `nakli-hub` build config). The CLI follows the same pattern. Browser tools register only the adapters they need (to keep bundle small).

### v1.x model: out-of-tree (proposed)

For v1.x, adapters MAY ship as separate npm/Go modules under `@naklitechie-adapters/*`. The fabric loads them dynamically. This requires:
- A standard package layout
- Signature verification (adapters from unknown authors are dangerous)
- A registry (or curated catalogue at `naklitechie.com/adapters`)

For v1.0, we keep adapters in-tree to control quality and security. Out-of-tree adapters come once the model has proven.

---

## v1.0 starter catalogue

Phase 1 ships with a deliberately small set:

### `courtlistener`
- **What:** Free-tier US court records via CourtListener REST API
- **Operations:** `search`, `get-opinion`, `get-docket`
- **Auth:** API key (free signup) or none for read-only with limits
- **Side effects:** None (read-only)
- **Idempotency:** Naturally idempotent
- **Used by:** Stance (legal positions audit); future research tools
- **CORS:** Open
- **Library:** plain `net/http` (Go) and `fetch` (JS); no SDK exists for this API

### `archive-org`
- **What:** Internet Archive APIs (Wayback Machine, search, item retrieval)
- **Operations:** `wayback-get`, `search`, `get-item`
- **Auth:** None (anonymous public APIs)
- **Side effects:** None
- **Idempotency:** Yes
- **Used by:** SansadSaar (gazette mirror sync), general archival tools
- **Library:** plain `net/http` and `fetch`

### `nasa-images`
- **What:** NASA's image APIs (mars-photos, image-and-video library)
- **Operations:** `mars-photos`, `search-images`
- **Auth:** API key (free, easy)
- **Side effects:** None
- **Idempotency:** Yes
- **Used by:** Topos (places), exploratory tools, demos
- **Library:** plain `net/http` and `fetch`

### `webhook-post`
- **What:** Generic HTTP POST to a configured URL
- **Operations:** `post`
- **Auth:** Whatever the target requires; passed via `headers` param
- **Side effects:** YES — the Grant MUST carry `requires-human-approval` OR `only-domain` caveat
- **Idempotency:** Required (caveat-enforced)
- **Used by:** Notifications, simple integrations
- **Library:** plain `net/http` and `fetch`
- **Notes:** Bare-bones; the spec calls out that this is the "everything fits if you're willing to assemble" adapter

### `email-resend`
- **What:** Send email via Resend.com (or any SMTP-API-style provider with the OpenAI-compatible-style generic adapter)
- **Operations:** `send`
- **Auth:** API key
- **Side effects:** YES — Grant MUST have `requires-human-approval` for unbounded use, or scoped to `only-domain` for known recipients
- **Idempotency:** Required (Resend supports idempotency keys natively)
- **Used by:** Bahi (invoice emails when explicitly approved), Stance audit notifications
- **Library:** plain `net/http` and `fetch`; Resend's API is small and well-documented

### `cloudflare-r2`
- **What:** Read/write S3-compatible R2 buckets in the user's own Cloudflare account
- **Operations:** `put-object`, `get-object`, `list-objects`, `delete-object`
- **Auth:** R2 API token
- **Side effects:** Yes for put/delete
- **Idempotency:** Yes (S3 PutObject is idempotent by path)
- **Used by:** Tijori (backup target), Folio (book library sync), general archival
- **Notes:** Same Cloudflare account as the Worker transport if user has both
- **Library:** Go: `github.com/aws/aws-sdk-go-v2/service/s3` (R2 is S3-compatible). JS: `@aws-sdk/client-s3` from AWS SDK v3, configured with R2 endpoint.

### `anthropic-claude` (also a remote-byok LLM route)
- **What:** Bridge-style access to Claude (separate from LLM primitive routing; useful when a tool wants direct provider access without the routing layer)
- **Operations:** `messages`
- **Auth:** Anthropic API key
- **Side effects:** Cost only (counts against billing)
- **Idempotency:** Anthropic's idempotency-key header
- **Notes:** Most tools should use the LLM primitive instead; this adapter exists for cases where the tool wants raw API access (e.g., for fine-grained prompt caching control)
- **Library:** Go: `github.com/anthropics/anthropic-sdk-go` (the official SDK is solid). JS: `@anthropic-ai/sdk` (official SDK).

### `openai-compatible`
- **What:** Generic adapter for OpenAI-compatible APIs (OpenRouter, Together, vLLM, LM Studio, etc.)
- **Operations:** `chat-completions`, `completions`, `embeddings`
- **Auth:** API key + base URL
- **Side effects:** Cost only
- **Notes:** Mirror of the LLM primitive adapter; same caveat as anthropic-claude
- **Library:** plain `net/http` and `fetch`. The OpenAI Go SDK has unnecessary baggage; the OpenAI JS SDK is reasonable but adds size. For an adapter this thin, raw HTTP is cleaner.

That's 8 adapters in Phase 1. Deliberately small. Each has a clear use case in the existing portfolio.

---

## Authoring a new adapter

The full guide lives in `fabric-sdk-go/bridge/adapters/AUTHORING.md` (to be written during M2). Outline:

### Step 1: Define the interface
- What's the service?
- What operations are needed?
- What credentials?
- Are operations side-effectful?
- What's the idempotency story?

### Step 2: Implement the adapter
Use the existing `courtlistener` adapter as a template. Approximate size: 100–300 lines per adapter, depending on complexity.

### Step 3: Tests
Each adapter MUST include:
- Unit tests with mocked HTTP responses
- An integration test (skippable in CI, runnable on demand with real credentials)
- Examples in the test file showing typical usage

### Step 4: Documentation
- A README in the adapter's directory
- Operations table
- Caveat recommendations (e.g., "for `email-resend send`, use `requires-human-approval` for unbounded use")
- Cost notes if applicable

### Step 5: Submission
- PR to `fabric-sdk-go` and/or `fabric-sdk-js`
- Bhai or a maintainer reviews
- Merged into the next release

For v1.0, NakliTechie owns the review bar. v1.x may open this up with a more formal process.

---

## Security posture

### Credentials never persist in the Hub or any transport

- BYOK credentials live in the user's FIF, encrypted with a separate key derived from the root
- When a Bridge call is made, the SDK decrypts the credential in memory, attaches it to the request, transmits it (over TLS to the transport, then via the adapter to the external service)
- The transport sees the credential in transit but does NOT persist it
- The external service sees the credential per its own policies (out of fabric's control)
- After the call: credential is zeroed from request memory

### Grant scoping is the security perimeter

Every Bridge call requires a Grant scoped to `bridge:call` with optional caveats:
- `only-domain in [<allowed domains>]` — adapter can only call these hosts
- `max-amount <= <cents> per <window>` — bounds financial side effects
- `requires-human-approval` — queue and wait for explicit approval per call
- `rate <= N per <window>` — bounds call volume

For agent-issued Grants (per D-Agents), these caveats are load-bearing. An agent with a `webhook-post` Grant lacking `only-domain` is effectively unbounded — the spec recommends adapters declare in their README which caveats are essentially required for safe agent use.

### Network egress

Adapters make HTTP requests to external services. This means:
- The Hub is the egress point (or the consumer's browser is, depending on transport)
- Operators concerned about data exfiltration should run the Hub behind an outbound firewall with allowlist
- The `only-domain` caveat is the user-facing version of this control

### Audit

Every Bridge call produces:
- A `bridge:call` event in the operation log (Hub/Worker side; not History)
- A History event if the tool explicitly emits one (e.g., Stance writes a `position-audit` event after a successful Bridge call to record what was verified)

For agent operations (per D-Agents), Bridge calls are particularly important to audit. The CLI command `nakli-cli bridge log --principal <agent-id>` surfaces an agent's recent Bridge activity.

---

## Versioning

Adapters use semver:
- `1.0.x` — bug fixes; no behavior change
- `1.x.0` — additive operations, backward compatible
- `2.0.0` — breaking changes (operation signatures, return shapes)

Tools declare the minimum adapter version they need:
```typescript
fabric.bridge.requireAdapter("courtlistener", "^1.0.0");
```

If the registered adapter doesn't satisfy: tool surfaces an error to the user.

When external services break (e.g., a v2 API release), adapter patch versions update; consumers transparently get the fix on adapter upgrade.

When external services deprecate operations (the courtlistener REST API switches from v3 to v4): adapter major version bumps; older versions stay supported in parallel for one major SDK cycle.

---

## Adapter discovery (protocol surface)

`GET /fabric/v1/bridge/adapters` returns the installed catalogue:

```json
{
  "ok": true,
  "data": {
    "adapters": [
      {
        "name": "courtlistener",
        "version": "1.0.2",
        "operations": [
          {
            "name": "search",
            "description": "Search opinions and dockets",
            "params": [
              { "name": "q", "type": "string", "required": true },
              { "name": "limit", "type": "integer", "default": 20 }
            ],
            "side_effects": false,
            "estimable": true
          }
        ]
      }
    ]
  }
}
```

This endpoint enables tools and agents to discover what's available without hardcoding.

---

## Dry-run support

Adapters MAY support `dry_run: true` for operations where:
- A cost estimate is possible without executing
- A validation step exists (e.g., "would this email send succeed given Resend's rules")

Dry run returns:
```json
{
  "ok": true,
  "data": {
    "would_succeed": true,
    "estimated_cost_cents": 5,
    "estimated_duration_ms": 200,
    "validations": [...]
  }
}
```

Dry runs do NOT count against rate limits or cost budgets. They are a UX affordance for "show me what would happen" before committing.

---

## Conformance

An adapter is "conformant" if:
1. It implements the interface (no panics on valid inputs)
2. Its operations match its declared schema
3. It honors `ctx` / `signal` for cancellation
4. It maps provider errors to the fabric error catalogue
5. It does NOT persist credentials
6. It does NOT make calls beyond declared operations
7. It does NOT modify the FIF or any other fabric state
8. It returns accurate metrics

The SDK includes a generic `bridge.ConformanceTest(adapter)` runner that verifies these properties with mocked HTTP.

---

## What about MCP?

The Model Context Protocol (MCP) emerging in late 2024–2026 is a related effort: a standard for LLM agents to call tools. There's a real question whether Bridge adapters should BE MCP servers, or just inspired by MCP.

For v1.0: Bridge adapters are NOT MCP servers. They're fabric-native. Rationale:
- MCP is designed for LLM-agent contexts; Bridge serves any consumer
- MCP's authorization model is different (it doesn't have macaroons)
- We control the interface end-to-end here, which matters for D-Agents commitments

For v1.x: explore exposing Bridge adapters via an MCP shim, so MCP-aware agents can use them. The shim translates MCP tool calls into Bridge calls. This makes the fabric play well in the MCP ecosystem without adopting MCP's semantics fabric-wide.

---

## Out of scope

- Streaming responses from adapters (Bridge calls are request/response; streaming is the LLM primitive's job)
- Adapter sandboxing / capability containment beyond Go's normal isolation (untrusted adapters are not supported in v1.0)
- Adapter hot-reload (restart the Hub to load new adapters)
- Adapter marketplaces with payments (v2+ thought; out of scope for now)
- Cross-adapter transactions (Bridge calls are independent; tools assemble multi-call workflows)

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md` (Bridge primitive)
- Hub spec: `hub-spec-001-v1.0.md` (Bridge call execution)
- LLM routing spec: `llm-routing-spec-001-v2.0.md` (parallel concept for inference)
- Decision D-Agents (Bridge caveats for agent use)
- Decision D-Origin (Grants are the only auth)
