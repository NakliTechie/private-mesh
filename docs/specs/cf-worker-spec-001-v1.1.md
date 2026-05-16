# nakli-cf-worker Specification

**Document:** `cf-worker-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `cf-worker-spec-001-v1.0.md` — adds explicit framework choice (Hono) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md`, `hub-spec-001-v1.0.md`
**Audience:** Implementers of `nakli-cf-worker`; users deploying the worker.

`nakli-cf-worker` is the zero-ops alternative transport: a Cloudflare Worker (~50–300 lines of TypeScript) backed by Cloudflare R2 for blob storage and Cloudflare KV for state. The user deploys it to their own Cloudflare account; NakliTechie has no operational role beyond providing the code.

This is the "I don't want to run a server" path. For users without an anchor yet, or as a fallback transport alongside the Hub. Same Fabric Protocol, different operational shape.

---

## Scope

This document specifies:
- Worker code structure
- Required Cloudflare bindings (R2, KV, optional D1)
- Configuration via Wrangler
- Storage layout in R2/KV
- Protocol endpoint implementation differences vs Hub
- Deployment flow
- Cost envelope
- Conformance with `fabric-spec-001-v1.0.md`

Out of scope:
- Hosting on alternative providers (Vercel Edge, Deno Deploy, AWS Lambda) — those are future implementations following the same protocol
- Cloudflare-specific optimizations beyond the standard runtime
- Custom domains (operator's responsibility via Wrangler config)

---

## Why Cloudflare specifically

We chose Cloudflare as the v1.0 reference for the zero-ops path because:
- Workers have a generous free tier (100k requests/day)
- R2 has no egress fees and S3-compatible API
- KV is appropriate for the small-state needs (cursors, idempotency cache, peer state)
- Wrangler is mature and well-documented
- The combination is genuinely zero-ops for most personal-scale uses

Other implementations (Vercel + Vercel Blob, Deno Deploy + KV, etc.) are welcome under the same protocol but are not the v1.0 reference.

---

## Dependencies

### Required

- **Hono** (`hono.dev`, npm: `hono`) — routing framework. Hono is the dominant Workers framework in 2026: TypeScript-first, ~12 KB, Cloudflare-optimized, native support for Workers KV, R2, and Durable Objects bindings. Routes map directly: `app.post('/fabric/v1/vault/append', handler)`.
- **`@noble/ciphers`** — XChaCha20-Poly1305. Workers does not expose WebCrypto's full surface; @noble/ciphers works in the Workers runtime.
- **`hash-wasm`** — Argon2id. Works in Workers (uses WASM).
- **WebCrypto** — HKDF-SHA256, Ed25519 sign/verify, SHA-256. Available in Workers.
- **`@noble/hashes`** — only if WebCrypto's HKDF is unavailable in the Workers environment for some reason; otherwise WebCrypto wins.
- **`macaroon`** (npm) — same library as the JS SDK, for verification on the server side.
- **`ulidx`** — ULID generation.

### Recommended

- **`itty-router`** — Hono is recommended; itty-router is an even-lighter alternative (~500 bytes) if a single deployer prefers minimum footprint. Both are fine.

### Forbidden

- Heavier frameworks (Express, Fastify, Koa). They don't run in the Workers runtime cleanly and would add unnecessary surface.
- Custom macaroon implementation. Reuse the `macaroon` npm package; wire-compat with Hub is non-negotiable.

### Notes on the Workers runtime

- The Worker runs in V8 isolates, not Node.js. Stdlib differences apply (no `fs`, no `net`, etc.).
- Hono handles Request/Response idiomatically.
- R2 access is via the env binding (`env.R2_BUCKET.put(key, value)`), not an SDK.
- KV access is via the env binding (`env.KV.get(key)`, `env.KV.put(key, value, options)`).
- The Worker has a 50ms CPU time limit per request (Free plan) or 30 seconds (Paid). Sync operations within a request must respect this; long operations queue.

---

## Distribution and licensing

- Repository: `github.com/naklitechie/nakli-cf-worker`
- License: Apache-2.0 (or MIT — TBD; same as rest of stack)
- Single TypeScript source: `src/worker.ts` (~200 lines plus type definitions)
- Build: `wrangler deploy` (esbuild via Wrangler)
- Versioning: independent of Hub binary; tagged releases match protocol versions

---

## Required Cloudflare bindings

The Worker requires:

```toml
# wrangler.toml

name = "nakli-fabric"
main = "src/worker.ts"
compatibility_date = "2026-01-01"
compatibility_flags = ["nodejs_compat"]

[[r2_buckets]]
binding = "BLOBS"
bucket_name = "nakli-fabric-blobs"  # user creates; pre-existing OK

[[kv_namespaces]]
binding = "STATE"
id = "<user's KV namespace ID>"

[vars]
HUB_ID = "01HMXYZ..."  # ULID; generated on first deploy
PROTOCOL_VERSION = "naklimesh/1.0"
MAX_EVENT_SIZE_BYTES = "1048576"
IDEMPOTENCY_RETENTION_SECONDS = "86400"
DISCHARGE_TTL_SECONDS = "3600"
LOG_LEVEL = "info"

[secrets]
HUB_PRIVATE_KEY = "<base64; encrypted by Cloudflare>"  # set via 'wrangler secret put'
HUB_PUBLIC_KEY = "<base64>"
```

The user's setup is:
1. Create R2 bucket: `wrangler r2 bucket create nakli-fabric-blobs`
2. Create KV namespace: `wrangler kv:namespace create STATE`
3. Generate Hub identity (using `fabric-sdk-go` or `fabric-sdk-js` locally): `nakli-cli generate-hub-identity > hub-identity.json`
4. Set secrets: `wrangler secret put HUB_PRIVATE_KEY < hub-identity.json.private`
5. Deploy: `wrangler deploy`

Total setup: 3-5 minutes for a moderately technical user.

---

## Storage layout

### R2 (blobs)

- Object key format: `blobs/<namespace>/<stream_id>/<event_id>.bin`
- Object content: raw ciphertext bytes
- Metadata (R2 object metadata):
  - `x-fabric-event-kind`: event kind
  - `x-fabric-appended-at`: RFC3339
  - `x-fabric-appended-by`: principal ID
  - `x-fabric-sequence-number`: sequence number (integer)
- Lifecycle policy: none (events persistent)

### KV (state)

Key prefixes:

- `principal:<principal_id>` — JSON: `{ type, public_key, parent_id, display_name, retired_at, retirement_event_id }`
- `stream:<namespace>:<stream_id>` — JSON: `{ stream_type, head_event_id, head_event_hash, event_count, sequence_counter }`
- `stream_index:<namespace>` — JSON array of stream_ids in this namespace (for ListStreams)
- `event_index:<namespace>:<stream_id>:<sequence>` — JSON: `{ event_id, blob_key, metadata }` (allows range queries by sequence)
- `idempotency:<grant_id>:<key>` — JSON: `{ payload_hash, response_status, response_body, expires_at }` (TTL set on KV write)
- `grant:<grant_id>` — JSON cache of known Grants (revocation state)
- `peer:<peer_id>` — JSON: `{ url, public_key, sync_state, last_seen }`
- `pending:<pending_id>` — JSON: pending Bridge operation
- `pairing:<token>` — JSON: pairing token state (TTL: 10 min)
- `pairing_code:<numeric>` — single-string value = pairing token (TTL: 10 min)

KV limits to be aware of:
- Value size: 25 MB max (we stay under via blobs in R2)
- Key size: 512 bytes max
- Reads: free (heavily cached at edge)
- Writes: rate-limited (1/s per key globally)

The KV-write limit is the main constraint for this transport. We mitigate by:
- Writing event blobs to R2 (no per-second limit)
- Updating stream heads only via single KV write per append (within the rate limit at personal scale)
- Idempotency cache write goes to KV but is rare relative to read

If a user's Workload exceeds KV write limits, they should run the Hub instead.

### Alternative: D1

Operators may opt into D1 (Cloudflare's SQLite-on-edge) instead of KV for the relational state. D1 has higher write throughput and supports the schema close to the Hub's SQLite. The Worker code is structured to allow either backend; the `wrangler.toml` declares which.

For v1.0 default, KV is the recommended backend (simpler, free tier covers most users).

---

## Worker code structure

```typescript
// src/worker.ts (simplified outline)

import { Macaroon, verifyMacaroon } from "./macaroon";
import { encryptEvent, decryptEvent } from "./crypto";
import { computeEventHash, verifyHashChain } from "./history";

export interface Env {
  BLOBS: R2Bucket;
  STATE: KVNamespace;
  HUB_ID: string;
  HUB_PRIVATE_KEY: string;
  HUB_PUBLIC_KEY: string;
  PROTOCOL_VERSION: string;
  MAX_EVENT_SIZE_BYTES: string;
  IDEMPOTENCY_RETENTION_SECONDS: string;
  DISCHARGE_TTL_SECONDS: string;
  LOG_LEVEL: string;
}

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    // CORS preflight
    if (req.method === "OPTIONS") return corsResponse();

    // Route
    const url = new URL(req.url);
    const path = url.pathname;
    const method = req.method;

    try {
      // Unauthenticated endpoints
      if (path === "/fabric/v1/health" && method === "GET") return await handleHealth(env);
      if (path === "/fabric/v1/discover" && method === "GET") return await handleDiscover(env);
      if (path === "/fabric/v1/identity/pair/complete" && method === "POST") return await handlePairComplete(req, env);
      if (path === "/fabric/v1/grant/discharge" && method === "POST") return await handleDischarge(req, env);

      // Authenticated endpoints: verify Grant first
      const grant = await verifyGrantHeader(req, env);
      if (!grant.ok) return errorResponse(grant.code, grant.message, 401);

      // Idempotency for state-changing operations
      if (["POST", "PUT", "DELETE"].includes(method)) {
        const idemResult = await checkIdempotency(req, grant.grant, env);
        if (idemResult.replay) return idemResult.response;
      }

      // Dispatch by path
      switch (true) {
        case path === "/fabric/v1/identity/principal" && method === "GET":
          return await handleIdentityPrincipal(grant.grant, env);
        case path === "/fabric/v1/identity/pair/initiate" && method === "POST":
          return await handlePairInitiate(req, grant.grant, env);
        case path === "/fabric/v1/identity/agents/provision" && method === "POST":
          return await handleAgentProvision(req, grant.grant, env);
        case path === "/fabric/v1/identity/agents/retire" && method === "POST":
          return await handleAgentRetire(req, grant.grant, env);
        case path === "/fabric/v1/vault/append" && method === "POST":
          return await handleVaultAppend(req, grant.grant, env);
        case path.startsWith("/fabric/v1/vault/stream/") && method === "GET":
          return await handleVaultRead(req, grant.grant, env);
        case path.startsWith("/fabric/v1/vault/streams/") && method === "GET":
          return await handleVaultListStreams(req, grant.grant, env);
        case path === "/fabric/v1/vault/subscribe" && method === "POST":
          return await handleVaultSubscribe(req, grant.grant, env);
        case path === "/fabric/v1/history/append" && method === "POST":
          return await handleHistoryAppend(req, grant.grant, env);
        // ... other endpoints
        default:
          return errorResponse("not_found", "Endpoint not found", 404);
      }
    } catch (err) {
      console.error("Worker error", err);
      return errorResponse("unavailable", "Internal error", 503);
    }
  }
};

// Helpers (each <30 lines):
async function corsResponse(): Promise<Response> { /* ... */ }
async function verifyGrantHeader(req: Request, env: Env): Promise<{ ok: boolean; grant?: Grant; code?: string; message?: string }> { /* ... */ }
async function checkIdempotency(req: Request, grant: Grant, env: Env): Promise<{ replay: boolean; response?: Response }> { /* ... */ }
async function handleVaultAppend(req: Request, grant: Grant, env: Env): Promise<Response> { /* ... */ }
// etc.

function errorResponse(code: string, message: string, status: number): Response { /* ... */ }
function successResponse(data: any, freshness: Freshness): Response { /* ... */ }
```

Approximate line count by section:
- Imports + types: 30
- Top-level fetch + routing: 50
- Auth + idempotency helpers: 40
- Vault endpoints: 60
- History endpoints: 50
- Sync endpoints: 40
- Bridge endpoints: 30
- LLM endpoints: 20
- Identity / pairing endpoints: 40
- Response helpers + CORS: 20

Total ~380 lines including types and helpers. The "50–200" range in the vision doc was optimistic; closer to 300–400 is realistic with full conformance.

---

## Key implementation differences vs Hub

### No filesystem, no SQLite

- R2 replaces blob filesystem; same content-addressed scheme
- KV replaces SQLite; key-value access patterns differ from SQL

### No long-running connections

- No SSE in Workers (Worker invocations are stateless and time-limited; Cloudflare supports SSE on Workers but with constraints)
- For `POST /fabric/v1/vault/subscribe`: Worker uses Cloudflare's native SSE/Durable Objects support (or polling fallback). v1.0 implementation MAY use Durable Objects for subscriptions; if not, consumers poll.

### No local sync between Workers

- Multi-transport sync (Worker syncing with Hub, etc.) is initiated by clients pushing to both transports, not by the Worker pulling
- This is a simpler model; the Worker is purely request/response

### Rate limits

- Cloudflare's free tier: 100k requests/day, 10ms CPU per request
- Workers Paid: 10M requests/month, 50ms CPU per request
- KV writes: 1 per second per key globally
- These constrain personal-scale only; for higher loads, operator runs the Hub

### Crypto operations

- Worker runtime supports SubtleCrypto (Ed25519, HMAC-SHA256, etc.)
- For ChaCha20-Poly1305: may use noble-ciphers (bundled via esbuild) since SubtleCrypto doesn't expose it universally
- All crypto is in-Worker; secrets (HUB_PRIVATE_KEY) are encrypted at rest by Cloudflare

### Bridge calls

- Worker can call external APIs via `fetch()`
- Latency added by edge-to-origin hop
- Idempotency cache prevents duplicate Bridge calls
- For Grants with `requires-human-approval`: pending operation persists in KV; approval flow stores result back; original client polls for completion

---

## Deployment flow

```bash
# One-time setup
git clone https://github.com/naklitechie/nakli-cf-worker
cd nakli-cf-worker
npm install
wrangler login

# Create infrastructure
wrangler r2 bucket create nakli-fabric-blobs
wrangler kv:namespace create STATE

# Generate Hub identity (using SDK or CLI locally)
nakli-cli generate-hub-identity > hub-identity.json

# Configure wrangler.toml (update KV namespace ID, customize as needed)
$EDITOR wrangler.toml

# Set secrets
cat hub-identity.json | jq -r .public_key | wrangler secret put HUB_PUBLIC_KEY
cat hub-identity.json | jq -r .private_key | wrangler secret put HUB_PRIVATE_KEY

# Deploy
wrangler deploy
```

After deploy: `nakli-cli pair-transport https://nakli-fabric.<account>.workers.dev` to register this transport in the user's FIF.

---

## Cost envelope

For personal/family scale (1-10 users, modest event counts):

- Workers free tier: 100k requests/day → sufficient for typical use
- KV: 100k reads/day free, 1k writes/day free → check the math
  - Worst case per Vault append: 3 KV reads (Grant verify, stream head, idempotency check) + 2 KV writes (stream head update, idempotency record)
  - 1000 appends/day = 3000 KV reads + 2000 KV writes
  - Within free tier for KV reads; just over the 1k/day KV write threshold
  - Workers Paid ($5/month) covers KV writes ($0.50 per million writes, so trivial)
- R2: 10 GB storage free, no egress fees → check the math
  - 10 GB = 10 million 1KB events. Plenty.

**Realistic estimate:** zero or $5/month for typical personal use. For families: still in free tier or $5/month. For small teams (10–25 users): $5–25/month depending on activity.

For higher scale: run the Hub instead. The Worker is the convenient path, not the scale path.

---

## Differences from Hub-deployed peers

Users may run both: a Hub on their anchor (primary), plus a Worker as fallback. The Worker is in their transport list with lower preference. When the anchor is unreachable, Worker handles operations and the queue catches up when the anchor returns.

Sync between Hub and Worker is initiated by clients pushing to both (per "no local sync" above). v1.x may add Worker→Hub pull sync if needed.

---

## Logging and observability

- `console.log` / `console.warn` / `console.error` → captured by Wrangler tail / Cloudflare dashboard
- Workers Analytics Engine (optional): operator binds an Analytics Engine dataset for metrics
- No external telemetry; logs stay in the user's Cloudflare account

---

## Security posture

What the Worker holds in the clear:
- Hub keypair (encrypted by Cloudflare at rest as a secret)
- Principal public keys, stream metadata, idempotency cache (in KV)
- Cloudflare logs (subject to Cloudflare's data policies)

What the Worker never sees:
- User passphrases
- Per-namespace encryption keys
- Bridge credential plaintext
- Event payload plaintext

Sovereignty story:
- The Worker runs in the user's Cloudflare account (not NakliTechie's)
- R2 bucket is the user's
- KV namespace is the user's
- Hub keypair is the user's
- Cloudflare sees ciphertext + metadata, like any cloud transport
- The user can export everything (R2 has S3-compat API; KV has bulk export) and move to a Hub at any time

Trade-off explicitly:
- Sovereignty is "lower than Hub" because Cloudflare sees metadata (which streams, which principals, when, how much)
- But still "higher than SaaS" because no NakliTechie involvement, no NakliTechie access, no NakliTechie billing
- User accepts this trade-off in exchange for zero-ops

---

## Conformance

`nakli-cf-worker` MUST pass all 32 tests in `fabric-spec-001-v1.0.md` conformance suite. The test suite is run against the deployed Worker:

```bash
nakli-cli conformance --target https://nakli-fabric.<account>.workers.dev --grant <conformance-grant>
```

CI runs the suite against a test deployment on every commit. Failures block release.

Known limitations vs Hub (these MUST NOT be conformance failures):
- Subscribe via SSE may have higher latency or use polling on free tier
- KV write limits constrain sustained throughput
- No multi-Hub sync (Worker is a "leaf" transport)

These limitations are documented; tests are written to be tolerant where the spec permits.

---

## Updating

```bash
git pull
wrangler deploy
```

The Worker has no state to migrate (state lives in KV/R2 which persist across deploys). Schema changes are versioned in KV value structures; the Worker code handles backward compat for at least one major version.

---

## Out of scope for v1.0

- Custom domains setup (use Wrangler / Cloudflare dashboard)
- Cloudflare Access integration for additional auth (Grants are the auth)
- Cloudflare Queues for the operation queue (consumers handle their own queues)
- Workers AI integration for local-ish inference (Phase 2; LLM primitive routing)

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md`
- Hub spec: `hub-spec-001-v1.0.md` (the canonical implementation)
- Cloudflare Workers docs: https://developers.cloudflare.com/workers/
- Wrangler: https://developers.cloudflare.com/workers/wrangler/
- Decisions: D5 (transport plurality)
