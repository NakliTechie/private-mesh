# Reuse Audit

**Document:** `reuse-audit-2026-05.md`
**Status:** Audit, May 2026
**Purpose:** Identify production-grade libraries and projects the Private Mesh can adopt as-is, with minimal wrappers, to save the agent rewrite effort.
**Audience:** Bhai, the coding agent doing Phase 1.

The principle: **specs define behavior, not implementation**. Wherever a battle-tested library produces output that matches a spec's wire format or behavior, the agent uses the library and writes a thin adapter rather than reimplementing. The spec stays the contract; the library is one valid implementation behind it.

This audit is per-spec. For each spec, I identify: what to reuse, what to wrap, what to actually write.

---

## Big wins (substantial work saved)

### 1. Macaroons — JS and Go

The whole macaroon implementation, including the libmacaroon-v2 binary format, base64 wire encoding, HMAC-SHA256 signature chain, first-party caveats, third-party caveats with discharge, and verification — exists and is mature.

**Go: `gopkg.in/macaroon.v2` (and `gopkg.in/macaroon-bakery.v3`)**
- The core library is from Canonical/Juju, ~10 years of production use.
- Wire-compatible with libmacaroon C, Python, and JS implementations.
- The `bakery` layer provides higher-level operations (mint, attenuate, verify with discharges).
- Apache 2.0 licensed (compatible with our license).
- Implementation: ~6-8 files in `fabric-sdk-go/grant/` are now ~200 lines of wrapper around `macaroon` + `bakery`, not ~1500 lines of original code.

**JS: `js-macaroon` (npm: `macaroon`)**
- Maintained by `go-macaroon` team, wire-compatible with the Go library.
- Supports v1 and v2 macaroons, JSON and binary formats.
- Plain ESM module, ~30 KB.
- BSD-3-Clause licensed.

**What we keep custom:**
- Our `identifier` JSON shape inside the macaroon (the v1.0 spec defines this).
- Our caveat vocabulary (`time < ...`, `rate <= ...`, `max-amount <= ...`, etc.) — caveats are just strings to the library; we parse and verify them in our `check` function.
- The `Grant.Mint`, `Grant.Verify`, `Grant.Attenuate` API surface we expose to consumers.

**What changes in the spec:** `fabric-sdk-go-spec-001-v1.0.md` and `fabric-sdk-js-spec-001-v1.0.md` should both name the upstream library explicitly. Spec language goes from "implement libmacaroon-v2 serialization" to "use `gopkg.in/macaroon.v2` and `js-macaroon` for serialization and signature verification."

**Estimated effort saved:** 3-5 sessions of M1 work.

---

### 2. NetBird embed library — for the v2.0 mesh layer

NetBird (which we already chose to wrap per D9) now exposes a Go embed package (`github.com/netbirdio/netbird/client/embed`) — released May 2026. This is exactly tsnet-shaped: embed the entire NetBird client in your Go binary. No separate installation. Includes Dial/Listen, exposed-service support, full WireGuard mesh participation.

**What this means for `mesh-netbird-spec-001-v2.0.md`:**
- Our `nakli-mesh` binary becomes a very thin wrapper around `embed.Client` — minting NetBird configurations from our principal IDs, then handing off all networking to the embed package.
- We do not need to author management-service integration, signaling, NAT traversal, or WireGuard tunnel management. NetBird handles all of it.

**License:** BSD-3-Clause (compatible).

**Estimated effort saved:** the entire mesh-netbird Phase 2 spec was estimated at "several sessions" for the wrapping. With the embed library, it's roughly one session to write the spec-mandated identity reconciliation and topology configuration, plus one for tests. About 2 sessions total instead of 5-6.

---

### 3. WebCrypto for FIF — JS side, almost zero custom crypto

The JS SDK's FIF implementation is largely WebCrypto API calls:
- XChaCha20-Poly1305: WebCrypto doesn't natively support XChaCha (only ChaCha20-Poly1305). Use **`@noble/ciphers`** (audited, dependency-free, ~10 KB) for XChaCha20-Poly1305.
- Argon2id: use **`hash-wasm`** (Argon2id in WASM, fast, well-maintained).
- Ed25519 signatures: WebCrypto supports Ed25519 in all modern browsers (since 2024).
- HKDF-SHA256: WebCrypto native.

**What we keep custom:** the FIF envelope structure, the lifecycle operations, the bridge credential second-layer encryption.

**Estimated effort saved:** 1-2 sessions of crypto plumbing.

---

### 4. Conformance suite scaffold — `httptest` and table-driven

The 32 conformance tests in `fabric-spec-001-v1.0.md` are exactly the shape that Go's standard `net/http/httptest` was built for. The pattern:
- A table of `{name, request, expected_response}` cases.
- One test runner that hits a target URL or an `http.Handler` directly.
- The same suite runs against the Hub, the Cloudflare Worker (via deployed URL), and any future transport.

No external library needed. Just `testing` + `httptest`.

**Estimated effort saved:** mostly a framing note — this was always going to be standard Go testing, but spelling it out keeps the agent from inventing a framework.

---

## Medium wins (meaningful but per-component)

### 5. Hub SQLite layer — `crawshaw.io/sqlite` or `mattn/go-sqlite3`

Two mature options. `crawshaw.io/sqlite` is cgo-free (pure Go via a custom build), good for cross-compilation. `mattn/go-sqlite3` is the workhorse, requires cgo.

**Recommendation:** `mattn/go-sqlite3`. The Hub is a daemon, not a distributed binary; cgo is fine. The library is ten years old, every Go developer knows it.

**What we keep custom:** the schema, the migration runner, the per-endpoint queries.

---

### 6. Hub HTTP server — `chi` router (or `net/http` + stdlib)

Two valid choices:
- **`net/http` with the new 1.22+ pattern matching** (e.g. `mux.HandleFunc("POST /fabric/v1/vault/append", ...)`). Zero dependencies. The "Go in 2026" recommendation in the searches points here.
- **`chi`** (github.com/go-chi/chi). Slightly nicer middleware composition. ~5K stars, very stable.

**Recommendation:** standard `net/http` with 1.22+ patterns. The Hub's routes are mostly direct mappings; middleware (Grant verification, idempotency, logging) composes naturally with the net/http style. No need for a framework.

---

### 7. Cloudflare Worker — Hono framework

**Hono** (`hono.dev`) is the dominant Workers framework in 2026. TypeScript-first, ~12 KB, Cloudflare-optimized, has built-in support for Workers KV, R2, and Durable Objects bindings.

The Cloudflare Worker spec assumes a single `worker.ts`. With Hono:
- Routing is `app.post('/fabric/v1/vault/append', handler)`.
- Middleware (Grant verification, idempotency) plugs in naturally.
- KV and R2 bindings come through the env parameter.
- Type-safe end to end.

**Estimated effort saved:** 1-2 sessions of routing/middleware plumbing. The original spec lands at ~380 lines TS; with Hono it's probably ~250 lines plus the framework.

---

### 8. mDNS — `grandcat/zeroconf` (Go) and built-in browser support

**Go: `github.com/grandcat/zeroconf`** is the canonical mDNS library. Pure Go, no dependencies. Supports both registering services and browsing.

**Browser:** there is no mDNS in browsers. The Local Network spec already acknowledges this — the browser uses our `nakli-local-bridge` binary as a sidecar that does mDNS and proxies to the browser via localhost HTTP. The bridge uses `grandcat/zeroconf` like everything else.

---

### 9. WebRTC for browser-to-browser — `pion/webrtc` only on the bridge side

The Local Network spec uses WebRTC for browser-to-browser peer connections. The browser side uses the **native WebRTC API** (no library needed). The bridge side that participates in the WebRTC fabric for peers behind NAT uses **`pion/webrtc`**, the canonical Go WebRTC implementation that NetBird itself depends on.

No library needed for browser code (native APIs). `pion/webrtc` for the bridge binary.

---

### 10. SSE for `vault/subscribe` — stdlib

Server-Sent Events is just `Content-Type: text/event-stream` and writing `data: ...\n\n` lines. Go's `net/http` handles it with `http.Flusher`. The browser side uses native `EventSource`. No library needed.

---

### 11. Idempotency middleware — small library or 50 lines custom

There are middleware libraries (`go-chi/idempotency`, etc.) but the logic is small enough that writing it ourselves is cleaner. The spec is precise: store key + payload hash + result for 24 hours. About 50-80 lines of Go in the Hub.

**Verdict:** write it ourselves. The spec is the spec.

---

### 12. PocketBase — for Stance (not Private Mesh)

Already in flight per Bhai's memory; mentioning for completeness. Stance uses PocketBase + Hetzner; that pattern doesn't transfer to the Private Mesh Hub (which is browser-native and has different semantics), but if at any point we want a managed Relay (Phase 4), PocketBase could be the chassis.

---

## Phase 1 tools — bigger libraries to consider for Saanjha

### 13. Saanjha (shared list) UI

The shared-list spec keeps the UI plain HTML/CSS/JS, no framework. That's the right call for v1.0 — the shape is "single HTML file, no build step." But a few small primitives that drop in:

- **Fractional indexing:** `fractional-indexing` (npm, ~3 KB) implements the algorithm cleanly. The spec mentions fractional indexing for list ordering; the library is the obvious choice.
- **ULID generation:** `ulidx` or `ulid` (npm). One-liner: `ulid()`. Don't write our own.
- **State management:** none needed. The whole list is small and re-renders on every change. If something more structured is wanted later, **`nanostores`** (~1 KB) is the right fit for our shape.

**What we keep custom:** all the actual UI, the conflict resolution UI, the Fabric SDK integration.

---

## What we genuinely write from scratch

These are the things no library covers cleanly, that are the actual product:

1. **The Fabric Protocol wire format** (the JSON shapes, the error codes, the freshness envelope). This is our spec; nobody else has this exact contract.
2. **The FIF format and lifecycle operations.** The envelope is custom enough that no library does it. The crypto inside is library-driven (point 3 above).
3. **The Vault and History primitives.** Event append, hash chains, namespace-scoped encryption — all on top of SQLite + WebCrypto, but the orchestration is ours.
4. **Idempotency middleware.** Small, spec-precise, ours.
5. **The Bridge adapter interface and the 8 starter adapters.** The interface is ours; each adapter is a thin wrapper around the named external API.
6. **The CLI command surface.** Standard Cobra or `urfave/cli` chassis under the hood, but the commands and their semantics are spec-defined.
7. **Saanjha — the shared list tool.** Entirely custom; this is the product.
8. **The conformance suite logic.** Test cases are spec-derived; the runner is standard `testing` + `httptest`.

---

## Spec adjustments to make this explicit

For the agent to use libraries cleanly without re-litigating, the specs should name them explicitly. Concretely:

### `fabric-sdk-go-spec-001-v1.0.md`
- Add to crypto/dependencies section: "MUST use `gopkg.in/macaroon.v2` for macaroon serialization and verification. The `bakery` package MAY be used for higher-level mint/discharge operations."
- "MUST use `golang.org/x/crypto` for Argon2id, HKDF-SHA256."
- "MUST use `golang.org/x/crypto/chacha20poly1305` for XChaCha20-Poly1305."
- "MAY use `github.com/oklog/ulid/v2` for ULID generation."

### `fabric-sdk-js-spec-001-v1.0.md`
- "MUST use `js-macaroon` (npm: `macaroon`) for macaroon serialization and verification. Compatibility with `gopkg.in/macaroon.v2` MUST be maintained."
- "MUST use WebCrypto for HKDF-SHA256, Ed25519 signing/verification."
- "MUST use `@noble/ciphers` for XChaCha20-Poly1305 (WebCrypto does not support XChaCha)."
- "MUST use `hash-wasm` for Argon2id."
- "MAY use `ulidx` for ULID generation."

### `hub-spec-001-v1.0.md`
- "MUST use `mattn/go-sqlite3` for SQLite access."
- "SHOULD use standard `net/http` with Go 1.22+ pattern matching for routing. MAY use `chi` if more complex middleware composition is needed."
- "MUST use `grandcat/zeroconf` if mDNS is added to the Hub (Phase 2)."

### `cf-worker-spec-001-v1.0.md`
- "SHOULD use Hono (`hono.dev`) as the routing framework. The framework is small (~12 KB) and well-suited to Workers."

### `local-network-spec-001-v1.0.md`
- "MUST use `grandcat/zeroconf` for mDNS in `nakli-local-bridge` and any Go consumer that announces/discovers."
- "MUST use `pion/webrtc` for the WebRTC signaling layer in `nakli-local-bridge`."
- "Browser code MUST use the native WebRTC API; no library."

### `cli-spec-001-v1.0.md`
- "MUST use `spf13/cobra` for the command framework. The spec's command structure maps directly to Cobra commands and subcommands."

### `bridge-adapters-spec-001-v1.0.md`
- Each adapter SHOULD use the canonical SDK for that service where one exists in Go and JS. Otherwise, plain `net/http` and `fetch`.
- Specifically: `anthropic-claude` adapter uses `github.com/anthropics/anthropic-sdk-go`; `openai-compatible` uses plain HTTP (the OpenAI Go SDK has too much baggage); `cloudflare-r2` uses `aws-sdk-go-v2/service/s3` (R2 is S3-compatible).

### `mesh-netbird-spec-001-v2.0.md`
- **Significant rewrite:** the spec was written assuming we'd wrap a NetBird CLI/agent. With the embed library, the spec should be reframed around `github.com/netbirdio/netbird/client/embed`. This drops complexity substantially.

---

## What this changes in the handoff

The agent handoff (v1.1) currently says "agent's call: specific Go and JS library choices when multiple satisfy requirements." That stays true, but for these named libraries, the agent should not deliberate — they are pre-chosen. Adding one line to the handoff:

> Where the specs explicitly name a dependency (e.g. `gopkg.in/macaroon.v2`), use that. Where they say "SHOULD use X", use X unless there's a concrete reason not to (and escalate the reason).

---

## Total estimated savings

Across Phase 1:
- Macaroon reuse: 3-5 sessions saved (M1 — protocol building blocks).
- WebCrypto + libraries for JS: 1-2 sessions saved (M5 — JS SDK).
- Hono for Worker: 1-2 sessions saved (M6 — Cloudflare Worker).
- Library-led approach across small primitives (ULID, fractional indexing, etc.): 1-2 sessions saved across multiple milestones.

**Total: roughly 6-11 sessions saved across Phase 1.** Down from 20-31 to 14-25 sessions.

Mesh layer (Phase 2): from 5-6 sessions to ~2. Not in Phase 1 but worth recording.

---

## One non-obvious recommendation: don't reuse Keyhive yet

Tempting given the ecosystem-survey finding, but: Keyhive is research-grade, just-landed-in-GAIOS, Rust-only. Adopting it now would mean:
- Cross-language binding work (Rust → Go, Rust → JS via WASM).
- Tracking an unstable API.
- Pinning our v1.0 to Keyhive's release schedule.

If Keyhive ships a stable 1.0 with cross-language bindings in late 2026 / early 2027, the v2 fabric protocol could adopt their convergent capabilities cleanly. For now, macaroons stay.

---

## Bottom line

The specs don't change. The wire contract doesn't change. About 30-40% of the agent's Phase 1 work was reimplementation of things that have library answers. Naming those libraries in the specs is a one-time edit that recovers several weeks of agent work.
