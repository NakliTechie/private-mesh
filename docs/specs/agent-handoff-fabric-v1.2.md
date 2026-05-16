# Fabric Phase 1 — Coding Agent Handoff

**Document:** `agent-handoff-fabric-v1.2.md`
**Status:** v1.2 normative; intended for the coding agent (Claude Code) executing Phase 1
**Supersedes:** `agent-handoff-fabric-v1.1.md` — adds dependency-naming clause, bumps spec versions to v1.1 across SDK/Hub/Worker/Local/CLI/Bridge specs (with explicit library choices baked in), bumps mesh spec to v2.1 (NetBird embed library), and adds the M0 restart directive.
**Companion to:** all specs in this directory
**Audience:** Coding agent receiving the spec set; Bhai when reviewing agent progress.

This is the handoff document for Phase 1 of the NakliTechie Private Mesh. The agent reads this first, then reads the listed specs as needed, then implements according to the milestone gates. This document tells the agent what's locked, what's the agent's call, when to stop, and how to know it's done.

**Read this end-to-end before writing any code.**

---

## RESTART NOTICE — read first

If you were previously working on Phase 1 using the v1.1 handoff and any of the v1.0 specs, **stop and restart from M0 with this v1.2 handoff and the new v1.1 specs.**

Why: an ecosystem survey identified several production-grade libraries that the v1.0 specs left as the agent's call. Those libraries are now named explicitly in the v1.1 specs, saving substantial reimplementation work (estimated 6-11 sessions across Phase 1). The cleanest path is to restart M0 with the right dependency manifests on day one, not to patch a partially-scaffolded repo with after-the-fact library swaps.

Specifically:
- Stop work on the current M0 if scaffolding has begun. Preserve no code; just abandon the working branch.
- Read this v1.2 handoff end-to-end first.
- Read the v1.1 specs (the v1.0 specs are archived under `docs/archive/` for reference but are not authoritative).
- Restart M0 with `go.mod` and `package.json` populated from the new "Dependencies" sections of each spec.

The protocol contract (`fabric-spec-001-v1.0.md`) did NOT change. The wire format is identical. Only the implementation dependencies were named.

---

## Dependency-naming clause

For Phase 1, where a spec's "Dependencies" section explicitly names a library:
- **MUST**: use that library; do not deliberate.
- **SHOULD / Recommended**: use that library unless there's a concrete documented reason not to. Escalate the reason in `STATUS.md` before substituting.
- **Forbidden**: do not use the listed forbidden libraries; do not roll your own implementations of the named ones.

Library choices that are NOT in any spec's Dependencies section remain the agent's call per the "Agent's call" section below. The agent picks, documents the choice in a code comment, and moves on. Don't ask Bhai about library choices outside the named set.

---

## What you're building

Phase 1 of the Private Mesh: a sovereign, browser-native, agent-aware capability fabric. The seven primitives (Identity, Grant, Vault, History, Sync, LLM, Bridge), three transports (Hub, Cloudflare Worker, Local Network), two SDKs (JavaScript and Go), one CLI, and one consumer tool (shared list).

The full vision is in `private-mesh-vision-001-v0.7.md`. The locked decisions are in `private-mesh-decisions-v0.7.md`. The wire protocol is in `fabric-spec-001-v1.0.md` — that's the most important document. Everything else implements or consumes it.

---

## Repository layout

One monorepo: `github.com/naklitechie/private-mesh`

```
private-mesh/
├── README.md                      # links to vision + decisions + this handoff
├── ARCHITECTURE.md                # architecture diagram + ASCII layer map
├── CONTRIBUTING.md                # how PRs work; how Bhai reviews
├── LICENSE                        # Apache-2.0 (TBD: confirm with Bhai before commit)
├── docs/
│   ├── vision-v0.7.html           # the unified vision doc (canonical)
│   ├── vision-v0.7.md             # markdown version
│   ├── decisions-v0.7.md
│   └── specs/
│       ├── fabric-spec-001-v1.0.md             # protocol (unchanged)
│       ├── fabric-sdk-js-spec-001-v1.1.md
│       ├── fabric-sdk-go-spec-001-v1.1.md
│       ├── hub-spec-001-v1.1.md
│       ├── cf-worker-spec-001-v1.1.md
│       ├── local-network-spec-001-v1.1.md
│       ├── cli-spec-001-v1.1.md
│       ├── bridge-adapters-spec-001-v1.1.md
│       ├── shared-list-spec-001-v1.0.md
│       ├── llm-routing-spec-001-v2.0.md        # Phase 2 (reference only in Phase 1)
│       ├── mesh-netbird-spec-001-v2.1.md       # Phase 2
│       ├── multi-anchor-spec-001-v2.0.md       # Phase 2
│       ├── fif-envelopes-spec-001-v1.x.md      # v1.x
│       ├── anomaly-detection-spec-001-v1.x.md  # v1.x
│       ├── ecosystem-survey-2026-05.md         # for context
│       ├── reuse-audit-2026-05.md              # for context
│       └── agent-handoff-fabric-v1.2.md        # this file
│   └── archive/                                # superseded v1.0 / v2.0 specs
├── fabric-sdk-go/                 # Go SDK (also publishable as module)
├── fabric-sdk-js/                 # JS SDK (also publishable as npm package)
├── fabric-merge-helpers/          # companion JS library
├── nakli-hub/                     # Hub binary
├── nakli-cf-worker/               # Cloudflare Worker
├── nakli-cli/                     # CLI binary
├── nakli-local-bridge/            # mDNS bridge for browser tools
├── saanjha/                       # shared-list tool (working name)
└── scripts/
    ├── build-all.sh
    ├── test-conformance.sh
    └── release.sh
```

Each subdirectory is self-contained: it has its own `README.md`, build/test entrypoint, and CI workflow.

---

## Phasing — Phase 1 vs Phase 2 vs v1.x

The spec set you're handed covers more than what you will implement. This is deliberate. Phase 1 ships a working sovereign capability fabric; Phase 2 and v1.x add depth. Knowing the boundary keeps you focused.

### What you implement in Phase 1

These 9 specs are the entire Phase 1 scope:

1. `fabric-spec-001-v1.0.md` — the protocol
2. `fabric-sdk-go-spec-001-v1.1.md` — Go SDK
3. `fabric-sdk-js-spec-001-v1.1.md` — JS SDK
4. `hub-spec-001-v1.1.md` — Hub binary
5. `cf-worker-spec-001-v1.1.md` — Cloudflare Worker
6. `local-network-spec-001-v1.1.md` — mDNS transport
7. `cli-spec-001-v1.1.md` — reference CLI
8. `bridge-adapters-spec-001-v1.1.md` — adapter interface + 8 starter adapters
9. `shared-list-spec-001-v1.0.md` — first consumer tool

### What exists in the spec set but you do NOT implement in Phase 1

These are Phase 2 (after v1.0 ships and soaks) or v1.x (extensions to v1.0 features):

- `llm-routing-spec-001-v2.0.md` — Phase 2. Anchor-local + browser-local + peer-routed routes. **Phase 1 implements only remote-BYOK** per the protocol's LLM endpoints; the routing layer is the simplest possible (one route, the BYOK provider the caller names). Three adapters in Phase 1: Anthropic, OpenAI, openai-compatible.
- `mesh-netbird-spec-001-v2.1.md` — Phase 2. NetBird wrapper. The fabric works without the mesh; consumers configure transports directly. Phase 1 ships without `nakli-mesh`.
- `multi-anchor-spec-001-v2.0.md` — Phase 2. Cluster + federation. Phase 1 is single-anchor, single-fabric. The protocol supports both, but the `/fabric/v1/cluster/*` endpoints are not implemented; they return 501 Not Implemented in Phase 1.
- `fif-envelopes-spec-001-v1.x.md` — v1.x. Distributed envelope types (shamir-shares, device-quorum, social-recovery). Phase 1 ships only `passphrase-only`.
- `anomaly-detection-spec-001-v1.x.md` — v1.x. The Hub does NOT include the anomaly engine in Phase 1. The operation_log table exists (it's a Phase 1 deliverable for audit); the engine sits on top.

You read these specs ONLY when (a) a Phase 1 decision needs forward-compatibility consideration, or (b) Bhai explicitly says "switch to Phase 2 work." Otherwise: do not implement, do not extrapolate, do not add scaffolding "in case" — that's premature.

### Forward-compatibility hooks Phase 1 MUST honor

A handful of v1.0 implementation details need to be aware of Phase 2 / v1.x without implementing them. Get these right in Phase 1 and the path forward stays open:

1. **FIF envelope_type enforcement.** The protocol reserves `shamir-shares`, `device-quorum`, `social-recovery`. v1.0 implementations MUST refuse FIFs with these types with a specific `fif_envelope_unsupported` error, not a generic parse failure. This is what makes the v1.x rollout safe — users get a clear error instead of mysterious corruption.

2. **LLM protocol endpoints exist; routing is minimal.** `/fabric/v1/llm/complete` and `/fabric/v1/llm/routes` are in the protocol. Implement them. The routing logic is trivial in Phase 1: read the caller's `preferred_route`, find the matching adapter in the BYOK set, call it, return. If `preferred_route` is `"auto"`: use the first available remote-BYOK route. No anchor-local, no browser-local, no fallback chain. Phase 2 adds the full routing algorithm.

3. **Bridge adapter discovery endpoint.** `GET /fabric/v1/bridge/adapters` MUST return the adapter catalogue per `bridge-adapters-spec-001-v1.1.md`. This is in Phase 1. The shape must be agent-discoverable from day one because agents will start consuming it immediately.

4. **Cluster endpoints reserved.** `/fabric/v1/cluster/*` paths return HTTP 501 `not_implemented` with a clear message. Do NOT return 404 (which would imply the URL was wrong rather than the feature being absent). Reserving the namespace prevents accidental URL conflicts later.

5. **Federation principal addressing.** v1.0 principal IDs are bare ULIDs. v2.0 introduces `<ulid>@<fabric-id>` form. Phase 1 parsers MUST accept (and ignore) the `@<fabric-id>` suffix on lookups — i.e. if a v1.0 Hub receives a request with principal `01HMX...@some-fabric.example`, it should match it against `01HMX...` and behave as if the suffix wasn't there. This lets future federated consumers send requests through legacy Hubs without breakage.

6. **`fabric.detections` History stream.** Phase 1 does not run the anomaly engine, but the namespace is reserved. Do not let user tools claim `fabric.*` namespaces. Reserve `fabric.detections`, `fabric.federation`, `fabric.cluster` — refuse Vault/History writes to these namespaces from non-Hub principals in Phase 1.

7. **Operation log retention.** v1.x anomaly detection will read 30-90 days of operation_log history. Phase 1's Hub MUST retain operation_log entries for at least 90 days by default (configurable). Don't aggressively prune.

8. **Idempotency key persistence.** v1.x features may rely on idempotency-key history beyond the 24-hour minimum. Hubs MAY retain keys longer (operator-configurable). Don't hard-cap at 24h.

That's the complete list. Everything else, ignore the Phase 2 / v1.x specs until told otherwise.

### What's still ahead of Phase 1 (not in the spec set yet)

The vision doc references work that isn't specified anywhere yet:
- `nakli-llm-server` (the anchor LLM daemon) — Phase 2 spec to come
- Browser-local backend implementations (Transformers.js, wllama integration) — tool-level, not platform
- Mobile consumers (iOS/Android) — out of scope through v2; the browser is the platform
- Anchor cluster bootstrap details beyond what's in `multi-anchor-spec-001-v2.0.md` — Phase 2 sub-spec when actually implementing

When these arrive, they'll be normal specs, not surprises.

---

## Build order (milestones)

Build in this sequence. Do not skip ahead. Each milestone ends with a working, tested artifact and is the gate to the next.

### M0: Skeleton — 1-2 sessions
- Monorepo scaffolded
- All directories with placeholder `README.md`
- Vision + decisions + specs committed under `docs/`
- License + contributing docs
- Empty CI workflows (one per subdirectory)
- One smoke test in each subdirectory that just prints "OK"

**Gate artifact:** `scripts/build-all.sh` runs all subdirectory smoke tests and prints OK.

### M1: Protocol building blocks — 2-3 sessions
The crypto and types layer used by everything.

In `fabric-sdk-go`:
- `crypto/` package (HKDF, Argon2id wrapping, ChaCha20-Poly1305 helpers)
- `identity/fif.go` (FIF parse, decrypt, serialize)
- `grant/macaroon.go` (libmacaroon-compatible serialization, verification)
- Unit tests for each (use known test vectors)

In `fabric-sdk-js`:
- Same as above for JS. WebCrypto first; noble-ciphers fallback for ChaCha20-Poly1305.

**Gate artifact:** unit tests pass; a FIF created in Go can be unlocked by JS and vice versa; a macaroon minted in Go verifies in JS and vice versa.

### M2: Hub binary — 3-5 sessions
The canonical transport implementation. Read `hub-spec-001-v1.1.md` end-to-end.

- SQLite schema migrations
- Each protocol endpoint (in `fabric-spec-001-v1.0.md`) implemented as an HTTP handler
- Macaroon verification middleware
- Idempotency middleware
- Operation log
- `health` endpoint
- `discover` endpoint
- Pairing flow
- systemd unit file + macOS launchd plist
- `nakli-hub backup` / `restore`
- `nakli-hub init`, `nakli-hub serve`

**Gate artifact:** `nakli-hub serve` starts; `curl http://localhost:7842/fabric/v1/health` returns ok; manually-crafted Grant + Vault append/read works end-to-end.

### M3: Conformance suite — 1-2 sessions
The 32 tests from `fabric-spec-001-v1.0.md`. Implemented in Go, in `fabric-sdk-go/conformance/`.

**Gate artifact:** `nakli-hub conformance --target http://localhost:7842` passes all 32 tests.

### M4: CLI — 2-3 sessions
Read `cli-spec-001-v1.1.md` end-to-end. Build on the now-stable Go SDK.

- All commands in the spec
- JSON output mode for all commands
- Config file + env var support
- Shell completions

**Gate artifact:** `nakli-cli init` produces a working setup; `nakli-cli vault append/read` against the Hub works; `nakli-cli conformance` passes.

### M5: JS SDK — 2-3 sessions
Read `fabric-sdk-js-spec-001-v1.1.md` end-to-end.

- All primitives with their helpers
- IndexedDB queue
- Multi-tab leader election via Web Locks
- Subscribe via SSE
- All public APIs as documented

**Gate artifact:** a minimal browser tool consuming the SDK can append to and read from the Hub. The SDK passes its own behavioral tests in headless browsers (Chromium, WebKit, Firefox).

### M5.5: Bridge adapters — 2-3 sessions
Read `bridge-adapters-spec-001-v1.1.md` end-to-end. The adapter interface + the 8 starter adapters.

- Adapter interface in `fabric-sdk-go/bridge/adapters/` and `fabric-sdk-js/bridge/adapters/`
- The 8 v1.0 adapters: `courtlistener`, `archive-org`, `nasa-images`, `webhook-post`, `email-resend`, `cloudflare-r2`, `anthropic-claude`, `openai-compatible`
- `GET /fabric/v1/bridge/adapters` returning the catalogue per spec
- `bridge.ConformanceTest(adapter)` generic runner
- Authoring guide as `fabric-sdk-go/bridge/adapters/AUTHORING.md`

**Gate artifact:** each of the 8 adapters' unit tests pass; the catalogue endpoint returns them; the conformance test runner verifies each.

### M6: Cloudflare Worker — 1-2 sessions
Read `cf-worker-spec-001-v1.1.md` end-to-end. Should be the smallest implementation (single TS file).

- Wrangler config
- All protocol endpoints
- R2 + KV usage as specified
- Deploy-ready

**Gate artifact:** Worker deployed to a test Cloudflare account passes the conformance suite.

### M7: Local Network transport — 2-3 sessions
Read `local-network-spec-001-v1.1.md` end-to-end. Most failure-resilient transport, but trickiest in browsers.

- Go: native mDNS announce/discover, HTTP server peer-to-peer
- JS: bridge integration (assumes the bridge binary is running)
- `nakli-local-bridge` binary: standalone bridge for browser-only consumers

**Gate artifact:** two Hubs on the same network discover each other and sync; one browser tool consuming the JS SDK with the bridge available can see other peers; conformance passes for local-only mode.

### M8: Shared list (Saanjha) — 4-5 sessions
Read `shared-list-spec-001-v1.0.md` end-to-end. Phase 1's flagship consumer.

Build in the 5-session sequence in that spec:
1. Skeleton + read-only with mock data
2. Fabric integration (single list)
3. Multi-list + UX polish
4. Operator surface + conflict UI
5. Polish + a11y + ship

**Gate artifact:** the success criteria in `shared-list-spec-001-v1.0.md` — 3-5 humans use it successfully across browsers/devices/offline scenarios without losing data. Bhai validates this manually with family members.

### M9: Release prep — 1-2 sessions
- Reproducible builds
- GPG signing
- `curl|bash` installer script (per D10)
- README.md polish, screenshot, quick-start
- Deployment of `list.naklitechie.com` (working name) on Cloudflare Pages

**Gate artifact:** end-to-end installation path documented and tested on a fresh machine.

Total: 14-25 sessions for Phase 1. The original v1.0 estimate was 20-31; the v1.1 specs name production-grade libraries explicitly (macaroon library, crypto, Hono, etc.), saving 6-11 sessions of reimplementation work. Bhai will likely run these across weeks/months, not days.

---

## What's locked vs what's the agent's call

### Locked (do not change without escalation)

These are NON-NEGOTIABLE. If implementation requires changing any of these, STOP and ask Bhai.

- **Wire protocol** (`fabric-spec-001-v1.0.md`) — every endpoint, header, JSON shape, error code, status code
- **All 32 conformance tests** — must all pass; cannot be skipped or weakened
- **Macaroon as Grant mechanism** (D1)
- **FIF envelope structure** (D2; passphrase-only in v1.0)
- **Three transports in v1.0** (D5: Hub, Worker, Local Network)
- **Six failure-model hooks** (D-Failure; all six must be present and functional)
- **Agents are first-class consumers AND principals with their own identity** (D-Agents)
- **All authority traces to human-issued root Grant** (Principle 11)
- **Server-side Grant enforcement at every transport** (non-negotiable 15)
- **Idempotency keys on every state-changing operation** (Hook 4, upgraded under D-Agents)
- **Single-HTML deployment for consumer tools** (NakliTechie shape)
- **Zero account, zero telemetry, zero server-required for consumer tools** (NakliTechie shape)
- **BYOK never persisted** (Principle 6)
- **`curl|bash` installer in v1.0** (D10)
- **No analytics, no phone-home** anywhere

### Agent's call (decide and document, don't ask)

These are internal choices the agent should make and document in code comments / commit messages:

- Variable names, function names, internal struct names
- Code organization within a single file (when not specified)
- Test names and test fixture data
- Specific Go and JS library choices when multiple satisfy requirements (e.g., which mDNS library)
- Implementation strategies for performance (caching, batching, indexing)
- Error message wording (must be informative; specific wording up to agent)
- Debug/log message content
- README structure and prose (must cover what users need to know)
- Comments and inline documentation style
- Git commit message style (use Conventional Commits unless Bhai says otherwise)

### Genuine ambiguity (stop and ask)

If you encounter genuine product-level ambiguity that changes user experience or commitments:

- A spec is internally contradictory
- Two specs contradict each other in a way that matters
- A spec doesn't cover a case you need to handle and the alternatives have meaningfully different user impact
- You're about to take a dependency on a library that isn't in the standard catalog
- You realized something the specs missed that's load-bearing

For these: stop, write a short clear question (a few sentences max; Bhai is terse), and wait for direction. Do not guess.

---

## Hard NOTs

- **Do NOT add telemetry**, error reporting (Sentry etc.), analytics (any), or auto-update without explicit user consent. The platform is private-by-default.
- **Do NOT bundle pre-trained models** in any binary; LLM routing fetches them at runtime per the user's config.
- **Do NOT add a "sign in with X" flow** anywhere. Identity is FIF; no exceptions.
- **Do NOT add email anywhere.** Per Stance decision pattern: no email collection, no email verification, no email anything.
- **Do NOT add Stripe / payments** to any v1.0 artifact. (Monetization is Phase 4; see D-Monetization.)
- **Do NOT mention NakliTechie's monetization** in user-facing strings.
- **Do NOT bundle large dependencies** that could be in the user's browser or system (jQuery, lodash whole, Material UI, Bootstrap, etc.). Keep wire size tight.
- **Do NOT use bundlers in deployed artifacts** for consumer tools. Single HTML, single deploy.
- **Do NOT add features beyond v1.0 scope** even if "they're easy" — v1.0 is bounded for a reason. Note them in `IDEAS-RUNNING.md` for later.
- **Do NOT change a NakliTechie tool's name** that's already in the wild without checking with Bhai.
- **Do NOT add a Service Worker** that aggressively caches; offline behavior should be predictable and explicit (the SDK's queue handles offline state).
- **Do NOT use Web Storage (localStorage/sessionStorage) in tools.** Per the artifact constraints; use IndexedDB or OPFS for state.
- **Do NOT use HTML forms** in React/JSX artifacts (carry-over from Bhai's portfolio style).

---

## Escalation protocol

When you need Bhai's input:

1. **Write a clear short question.** Bhai is extremely terse; he expects you to be too. One paragraph max, ideally two sentences. Include only what's needed to decide.
2. **Propose options if appropriate.** If there's a clear A/B/C, list them; Bhai often replies "B" without prose.
3. **Don't relitigate locked decisions.** If you're tempted to change something locked, the answer is almost always "no" and asking wastes time.
4. **Don't ask for permission to refactor.** If you see a way to clean up code that's clearly better, do it; mention it in the commit message.
5. **Don't ask about names.** Per standing rule: defer naming until launch. Use working names; Bhai picks the real name later.

Bhai's typical responses you should expect:
- "go" / "ok" / "yes" — proceed
- "no" / "skip" — don't do that
- A single letter — option selected from your list
- A single word — that's the answer; figure out what it means
- A two-line correction — read carefully; that's the new direction
- Silence for a while — Bhai is busy or asleep; keep working on non-blocking things

---

## Gate artifacts (per milestone)

For each milestone above, produce:

1. **Code merged to main** — passes all CI checks
2. **Smoke test script** — a single command that demonstrates the milestone works
3. **README update** — for the changed subdirectory, README.md describes how to build, run, and test
4. **Commit message** — references the milestone (e.g., "M3: conformance suite — 32/32 passing")
5. **One-paragraph summary in `STATUS.md`** at repo root, dated, that Bhai can scan

Do NOT mark a milestone done until all five exist. Do NOT proceed to the next milestone until the previous one is done.

---

## README scope (per subdirectory)

Each subdirectory's README.md MUST include:

- **One-line description** of what this directory is
- **Status** — alpha / beta / stable / archived
- **Quick start** — minimum steps to get something working
- **Build instructions** — how to build from source, what tools needed
- **Test instructions** — how to run tests
- **Configuration** — env vars, config files, defaults
- **Operational notes** — for binaries: how to deploy / install / upgrade
- **Security notes** — what this directory holds in clear vs encrypted; defense-in-depth
- **Roadmap** — what's coming next (v1.x, v2.0 hints)
- **License**

What NOT in READMEs:
- Marketing copy
- Phrases like "production-ready" or "enterprise-grade"
- Emoji in headers
- Excessive screenshots (one is fine; ten is bloat)
- Repeated info from vision/decisions docs (link to them instead)

---

## Design tokens and tool styling

For consumer tools (currently just shared-list in v1.0):

- **Type:** Inter Tight or system font stack (sans-serif); JetBrains Mono for code
- **Colors:** Rangrez palettes (vendored as JSON in `fabric-sdk-js` or as a peer dep)
- **Spacing:** 4px base unit; 8/12/16/24/32/48 scale
- **Borders:** 1px (default), 2px (emphasis); 4px / 8px radius
- **Motion:** subtle; <200ms; respect `prefers-reduced-motion`
- **Dark mode:** required; respect `prefers-color-scheme` by default with toggle override

Tools may diverge for good reason (the Creative Suite tools have stronger custom identity), but the shared list should feel familiar to anyone who's used a NakliTechie tool.

---

## Empty states and error states

Every tool MUST handle:
- **First-time use** — empty FIF, no streams yet → guide the user to setup
- **No connection** — show offline indicator; don't block UI; queue operations
- **Permission denied** — explain which Grant is missing in actionable terms
- **Conflict** — surface clearly with resolution UI
- **Hash chain broken** (History) — alert the user; do not silently continue

Each empty / error state should:
- Be explicit (no silent "nothing here")
- Be calm in tone
- Provide one clear next action
- Surface technical detail under a "details" disclosure for power users

---

## Security posture summary (for the agent's awareness)

- All payloads encrypted client-side with per-namespace keys
- Transports see ciphertext; verifying macaroons is the only trust boundary
- FIF is the user's responsibility; tools never copy it to "cloud" anywhere
- Bridge credentials are double-encrypted; live in FIF; decrypted in memory only
- Macaroon attenuation is enforced server-side at every transport — NOT client-side
- Idempotency keys are mandatory for state-changing operations
- Logs never contain payload content; only structural metadata
- No telemetry, no phone-home, no auto-update without consent

If you're about to implement something that touches these — slow down. Re-read the relevant spec section. If it's not specified, escalate.

---

## Agent operations under your own SDK

The Phase 1 SDK supports agent identities and Grant attenuation. The coding agent (you) running this work is itself a great test:

- Bhai may provision a Grant for the coding agent with scope limited to certain repos
- Agent operations on Fabric (e.g., reading from a private repo's notes stream) should follow D-Agents commitments
- This is meta — the platform you're building governs the agent building it — and it's a useful check

If you find yourself wanting to bypass Grant scoping for "developer convenience," that's a signal something's off. Use the protocol; benefit from the security guarantees you're building.

---

## What "done" looks like for Phase 1

When all 10 milestones are green:

- `nakli-hub` runs on Bhai's anchor; serves all 7 primitives
- `nakli-cf-worker` deployed to Bhai's Cloudflare account; serves as fallback
- Local Network transport works on Bhai's home network
- `nakli-cli` is installable via `curl|bash`
- `fabric-sdk-go` is `go get`-able
- `fabric-sdk-js` is `npm install`-able and CDN-available
- Saanjha (shared list, working name) is deployed and used by Bhai's family
- Conformance suite passes for all transports
- README + docs are accurate

At that point: Phase 1 is shipped. Phase 2 (LLM routing, anchor cluster, mesh layer) begins after a real-world soak period (4-8 weeks).

---

## When you're stuck

If you find yourself:
- Going in circles on a decision → escalate
- Adding TODOs faster than you're closing them → step back; address one TODO completely
- Writing more spec than code → you're past the spec phase; trust the specs and ship
- Wanting to refactor a third time → the second refactor was probably right; stop

The point is to ship Phase 1. Not to perfect Phase 1. Subsequent versions iterate; v1.0 just needs to work end-to-end and prove the architecture.

---

## Going to sleep

When Bhai goes silent, keep building. Work through non-blocking tasks in milestone order. If you finish all non-blocking work in a milestone and the next requires a decision, write a clear short question to `STATUS.md` and pick up another milestone if possible.

When Bhai returns, summary first: what you did, what's done, what's blocked. He'll triage.

---

## References

### Phase 1 specs (you implement these)

In priority order:

1. `fabric-spec-001-v1.0.md` — the wire protocol; the contract
2. `private-mesh-vision-001-v0.7.md` — what this is, who it's for, why this shape
3. `private-mesh-decisions-v0.7.md` — every locked decision with rationale
4. `fabric-sdk-go-spec-001-v1.1.md`
5. `fabric-sdk-js-spec-001-v1.1.md`
6. `hub-spec-001-v1.1.md`
7. `cf-worker-spec-001-v1.1.md`
8. `local-network-spec-001-v1.1.md`
9. `cli-spec-001-v1.1.md`
10. `bridge-adapters-spec-001-v1.1.md` (interface + 8 starter adapters)
11. `shared-list-spec-001-v1.0.md`

### Phase 2 specs (read for forward-compat only; do not implement)

12. `llm-routing-spec-001-v2.0.md` — full LLM routing; Phase 1 implements minimal subset per the forward-compat hooks
13. `mesh-netbird-spec-001-v2.1.md` — mesh layer; not implemented in Phase 1
14. `multi-anchor-spec-001-v2.0.md` — cluster + federation; not implemented in Phase 1 but endpoint paths reserved

### v1.x specs (read for forward-compat only; do not implement)

15. `fif-envelopes-spec-001-v1.x.md` — distributed FIF envelopes; v1.0 enforces refusal per forward-compat hooks
16. `anomaly-detection-spec-001-v1.x.md` — engine on top of operation_log; v1.0 retains operation_log appropriately

### Conflict resolution

Specifications conflict?
- `fabric-spec-001-v1.0.md` wins for wire format
- `private-mesh-vision-001-v0.7.md` and `private-mesh-decisions-v0.7.md` win for product shape and locked decisions
- Phase 2 / v1.x specs NEVER override Phase 1 specs for Phase 1 implementation choices
- When in genuine doubt: escalate.

---

Now go build.
