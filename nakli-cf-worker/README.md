# nakli-cf-worker

Cloudflare Worker transport for the NakliTechie Private Mesh. The zero-ops alternative to the Hub binary: a TypeScript Worker backed by R2 (blobs) and KV (state).

**Status:** alpha — **M6 complete.** Full protocol surface implemented in a single Worker (`src/worker.ts`). The 32-test conformance suite passes against `wrangler dev` (Miniflare-simulated R2 + KV). Deployed-to-Cloudflare conformance run is a one-step `wrangler deploy` + same conformance command, documented below.

## Quick start (local dev)

```sh
# 1. Install deps
pnpm install

# 2. Smoke
./smoke.sh                                    # typecheck

# 3. End-to-end conformance against wrangler dev (Miniflare)
../scripts/worker-gate.sh                     # reports 32/32 passing
```

## Quick start (Cloudflare deploy)

```sh
# 1. Authenticate
pnpm exec wrangler login

# 2. Create R2 bucket + KV namespace
pnpm exec wrangler r2 bucket create nakli-fabric-blobs
pnpm exec wrangler kv namespace create STATE
# → copy the namespace id into wrangler.toml's [[kv_namespaces]].id

# 3. Generate a Hub identity (using nakli-hub or nakli-cli)
../nakli-hub/nakli-hub init --data-dir ./hub-data

# 4. Set the secrets the Worker needs
jq -r .hub_id            hub-data/hub-identity.json | pnpm exec wrangler secret put HUB_ID
jq -r .public_key        hub-data/hub-identity.json | pnpm exec wrangler secret put HUB_PUBLIC_KEY
jq -r .private_key       hub-data/hub-identity.json | pnpm exec wrangler secret put HUB_PRIVATE_KEY
jq -r .macaroon_root_key hub-data/hub-identity.json | pnpm exec wrangler secret put MACAROON_ROOT_KEY

# 5. Deploy
pnpm exec wrangler deploy

# 6. Run conformance against the deployed Worker
../nakli-hub/nakli-hub conformance \
    --target https://nakli-fabric.<account>.workers.dev \
    --data-dir ./hub-data
```

## Architecture

Single-file Worker with three supporting modules:

| File | Purpose |
| --- | --- |
| [`src/worker.ts`](src/worker.ts) | Entry point + routing (~700 LOC); one handler per protocol endpoint |
| [`src/envelope.ts`](src/envelope.ts) | Success/error envelope helpers, CORS, freshness |
| [`src/macaroon.ts`](src/macaroon.ts) | Macaroon mint / parse / verify (wire-compatible with Hub via the `macaroon` npm package) |
| [`src/caveats.ts`](src/caveats.ts) | Caveat evaluation (mirrors Hub's caveat catalogue) |
| [`src/storage.ts`](src/storage.ts) | KV + R2 storage helpers (principals, streams, events, idempotency, grants, pending, pairing) |
| [`src/hash.ts`](src/hash.ts) | SHA-256 via WebCrypto |
| [`src/env.ts`](src/env.ts) | Wrangler bindings + constants |

The Worker uses raw `fetch`-style dispatch (no Hono routing layer) to keep the bundle small and the fast path predictable — every handler is ≤30 LOC. Hono is in deps for tools that want to embed Worker pieces.

### Storage layout

| Prefix | Shape |
| --- | --- |
| R2 `blobs/<ns>/<sid>/<event_id>.bin` | Raw ciphertext bytes; R2 customMetadata records kind / sequence / principal |
| KV `principal:<id>` | JSON principal record (type, retired_at, …) |
| KV `stream:<ns>:<sid>` | JSON stream head (event_count, head_event_hash) |
| KV `stream_index:<ns>` | JSON array of stream_ids in the namespace |
| KV `event_index:<ns>:<sid>:<padded_seq>` | JSON event record (event_id + metadata) |
| KV `idempotency:<grant_id>:<key>` | JSON idempotency record (TTL applied) |
| KV `grant:<grant_id>` | JSON grants_known cache (revocation state) |
| KV `pending:<id>` | JSON pending Bridge call |
| KV `pairing:<token>` / `pairing_code:<numeric>` | JSON pairing state (10-min TTL) |

### Implementation differences vs Hub

- **No filesystem, no SQLite.** R2 + KV replace both.
- **No SSE.** `vault.subscribe` is 501 in M6; SSE / Durable Objects integration lands at M6.x. Consumers should poll `vault.read` in the meantime.
- **No multi-anchor sync.** `sync.peers` returns empty; `sync.pull`/`push`/`conflict-ack` are 501.
- **No bridge adapter dispatch.** The Worker hosts only the inert `conformance-test` noop adapter (used by conformance tests 12 / 13 / 14 / 31). Real adapter execution belongs on the Hub; the Worker can call out via `fetch()` but the v1.0 reference deliberately keeps Bridge adapters out of the edge to avoid bloating the bundle.
- **`POST /fabric/v1/_conformance/setup`** is exposed only when `CONFORMANCE_MODE=true`; it seeds the retired-agent principal that conformance test 30 expects.

## Caveat enforcement

All thirteen caveat types are wired (mirroring the Hub's M3 implementation):
- `time <` / `time >`, `operation in`, `namespace ==`, `nondelegatable`, `idempotency-required`
- `principal-type in […]`, `agent-id ==`, `device-id ==` — cross-checked against `X-Fabric-Principal-Type` / `X-Fabric-Agent-Id` / `X-Fabric-Device-Id` request headers (else accepted as Hub-trusted)
- `rate <= N per <window>` — in-memory token bucket per `grant_id` *per isolate* (Cloudflare runs multiple isolates; documented limitation for v1.0)
- `max-amount <= <int> <currency>` — Bridge calls
- `only-domain in […]` — Bridge calls
- `requires-human-approval` — 202 + `pending_id`; row retrievable via `GET /fabric/v1/bridge/pending/{id}`
- `discharge-from <verifier>` — `POST /fabric/v1/grant/discharge` mints discharges; `X-Fabric-Discharge` header carries them

## Tests

```sh
pnpm exec tsc --noEmit         # typecheck (smoke.sh wraps this)
../scripts/worker-gate.sh      # end-to-end 32/32 conformance via wrangler dev
```

Unit tests via Vitest land in M6.x; the conformance suite is the primary gate.

## Security notes

- The Worker never sees user passphrases or per-namespace encryption keys. Vault payloads are encrypted client-side; the Worker stores opaque ciphertext in R2.
- `MACAROON_ROOT_KEY` is the only secret a deployed Worker holds. Cloudflare secrets are encrypted at rest. Generate via `nakli-hub init` or `nakli-cli generate-hub-identity` locally, then `wrangler secret put`.
- `CONFORMANCE_MODE=true` exposes `/fabric/v1/_conformance/setup`; this is gated specifically for the conformance gate and SHOULD remain `false` (or unset) in production.
- The Worker logs to Cloudflare's request log; that log stays inside the user's Cloudflare account.

## Roadmap

- M6 (done): full protocol surface, conformance via `wrangler dev` (32/32)
- M6.x: SSE `vault.subscribe` via Durable Objects, Vitest unit suite, real-deploy CI step
- M7+: multi-anchor sync (Sync APIs become live)

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
