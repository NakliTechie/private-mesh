# NakliTechie Private Mesh — Decisions Log

**Document:** `private-mesh-decisions-v0.7.md`
**Status:** Living document, updated as decisions are made or revised.
**Companion to:** `private-mesh-vision-001-v0.7.md`

---

## Guiding principles

Principles are the layer above decisions. When a new question comes up, these are what to reach for first. Every decision below should be consistent with these; if it's not, either the decision or the principle needs to change.

1. **Failure resistance is structural, not optional.** Power cuts, partitions, dead peers are operating conditions, not error conditions.
2. **The home is online even when the internet isn't.** Devices on the same network reach each other without going through anything external.
3. **Primitives are interface-agnostic.** HTML, native apps, CLIs — all first-class. The fabric speaks HTTP/JSON; any client that speaks HTTP/JSON is valid.
4. **Sovereignty over convenience, but convenience is not the enemy.** Deliver convenience where it doesn't compromise sovereignty.
5. **Visible to the curious, invisible to the unwilling.** Power users see internals; family members see the app.
6. **Open all the way down.** No proprietary components in the critical path.
7. **The user owns their failure modes.** No NakliTechie recovery service; user chooses their resilience model.
8. **The anchor is a peer, not the center.** The fabric works when the anchor is down.
9. **Local-first inference is the default; remote is a fallback.** User data does not leave the fleet without explicit opt-in.
10. **Every feature serves sovereignty, resilience, or both.** Features that serve neither are candidates for cutting.
11. **All authority flows downstream from a human-issued root.** No agent, no tool, no consumer holds capabilities that don't trace, through cryptographic attenuation, to a Grant a human signed. Agents derive; humans originate.

---

## Conventions

- **Status:** `Locked` (decided, not revisiting unless new information surfaces) / `Open` (still being decided) / `Revised` (was locked, has been changed) / `Deferred` (intentionally not deciding now)
- **Failure-model review:** Each decision has been re-examined under the constraint "the system is correct under continuous, asynchronous, partial failure." Notes added where the review surfaced refinements.

---

## D1 — Capability token format

**Status:** Locked
**Decision:** Macaroons
**Date locked:** Early in design conversation

**Rationale:**
- Mature (12+ years), well-understood, libraries exist
- Bearer tokens with attenuation via caveats — exactly what the fabric needs
- Third-party caveats support delegated revocation checks
- Wire format simple enough that an agent can implement encode/decode reliably without crypto risk

**Alternatives considered:**
- **UCAN** — modern, DID-based, designed for this use case. Rejected because ecosystem is smaller, more moving parts (DIDs add an identity model on top of keys), and macaroons are sufficient at personal-fabric scale.
- **Custom format** — rejected because designing crypto is where projects die.

**Failure-model review:** ✓ Holds. Macaroons verify offline. Caveats evaluated locally. No verification server required. Revocation-list caveat needs History to be reachable, but stale History is acceptable (bounded staleness, visible to user).

---

## D2 — Cross-origin identity model

**Status:** Locked
**Decision:** Multi-origin via user-owned Fabric Identity File (FIF)

**Rationale:**
- Original framings (single origin / per-tool origin with postMessage bridge) both leashed tools to NakliTechie's hosting
- The portfolio's defining shape is "download the HTML, run it anywhere — file://, intranet, your domain, my domain"
- A user-held FIF makes the fabric work at any origin without any "fabric origin" concept
- FSA API to read FIF from disk; passphrase to decrypt; tool gets identity for the session

**FIF structure (revised under failure-model review):** Layered envelope.
- Outer layer declares how to derive the decryption key
- Inner layer holds identity material (keys, transport configs, grants, optional cache)
- v1.0 ships `passphrase-only` envelope type
- v1.x adds `shamir-shares`, `device-quorum`, `social-recovery` envelope types
- Inner format unchanged across envelope versions

**Failure-model review:** ✓ Holds and strengthens. FIF is a local file — works offline. Layered envelope means v1.x can add distributed-recovery options without breaking v1.0 FIFs. Hooks for distributed FIF are present from day one.

---

## D3 — Conflict semantics for concurrent writes

**Status:** Locked
**Decision:** Fabric provides ordered event delivery; tools own merge semantics (Option B)

**Rationale:**
- Fabric stays unopinionated about data shape
- Matches existing portfolio (Tijori, Bahi, Stance, VaultMind are all event-sourced) without forcing future tools
- Keeps fabric responsibility crisp — transport and ordering, not data semantics
- A separate `fabric-merge-helpers` library provides common patterns (append-union, last-write-wins-per-key) for tools that want them

**Alternatives considered:**
- **Option A** — Fabric defines one merge model (append-union). Rejected as too opinionated for a portfolio likely to grow.
- **Option C** — Fabric provides multiple merge modes, tools pick. Rejected as duplicating Option A's cost without enough benefit over B+helper.

**Failure-model review:** ✓ Strengthens. The ordered-delivery contract is now explicitly the five CRDT-shaped properties: idempotent, order-tolerant, resumable, symmetric, eventual. Tools that use the helper library get correct behavior under partition; tools that roll custom merge inherit the same protocol-level guarantees.

---

## D4 — Revocation semantics

**Status:** Revised (was locked, refined under failure-model review)
**Decision:** Per-grant choice between opportunistic refresh and revocation list (Option C, revised)

**Original (v0.2):**
- Short expiry + refresh for session grants
- Revocation list for delegated grants

**Revised (v0.3):**
- **Opportunistic refresh** for grants on the user's own devices. Long-lived expiry (days/weeks). Holder refreshes opportunistically when network is available; under partition, existing grant keeps working. Network outage does NOT lock the user out of their own data.
- **Revocation list** for delegated grants to other parties. Longer expiry, list-backed revocation. Vault checks the list at use time; staleness is bounded and visible. Immediate revocation when list propagates; stale-list grace period accepted as a property of the system.

**Failure-model review:** The original "short expiry + refresh" framing assumed the holder could always reach the issuer to refresh. Under partition, this fails — the user's phone could lose access to their own data because home Wi-Fi is out. Revised so refresh is opportunistic (not gating), with long-lived underlying expiry. Session grants are now resilient to partition.

---

## D5 — Transport plurality

**Status:** Revised (was locked, expanded under failure-model review)
**Decision:** Transport Protocol is the spec; three reference implementations in v1.0

**Original (v0.2):**
- Two reference implementations in v1.0: Go Hub + Cloudflare Worker
- Local Network deferred to v1.1

**Revised (v0.3):**
- **Three reference implementations in v1.0:**
  1. `nakli-hub` — Go binary, self-hosted (anchor, VPS, Pi, anywhere always-on)
  2. `nakli-cf-worker` — Cloudflare Worker reference, ~50-200 lines TypeScript, BYO R2
  3. `nakli-local` — Local Network (mDNS) reference, browser-native, no external service
- Multiple implementations can coexist in one user's setup
- Conformance tests verify all implementations against the same spec
- Anyone can write more implementations (Vercel, Deno Deploy, Lambda, plain Node.js + filesystem, embedded)

**Failure-model review:** Local Network deserves first-class v1.0 status, not v1.1. It's the most failure-resilient transport (everything else can be down, this still works) and the most sovereign (no third party at any layer). For office scenarios (Mehfil-style group work on the same Wi-Fi) and household scenarios (everyone on home Wi-Fi during ISP outage), it's the correct default.

**Sub-decision: Hub language.** Go. Speed of execution, single binary, minimal dependencies, easy cross-compilation, mature ecosystem for server-side work.

---

## D6 — First fabric-native consumer tool

**Status:** Locked
**Decision:** Shared list (groceries, todos, packing lists)

**Rationale:**
- Multi-user pressure from day one (Bhai, son, brother editing the same household lists)
- Daily use is automatic — households need lists
- Simple data model (items + tick states as events)
- Multi-origin works naturally (kitchen iPad, phone, laptops)
- Concurrent writes from different devices/users naturally happen — exactly the scenarios the failure model has to handle
- If it works well, it's a portfolio-worthy tool

**Alternatives considered:**
- Shared notes — viable but less natural multi-user pressure
- Shared bookmarks — simpler than notes but less daily-use natural
- Multi-device journal — typical single-user, less stress on the fabric
- Something from running ideas list — open to revision but shared list is the cleanest test

**Failure-model review:** ✓ Near-perfect. Households genuinely go offline; family members genuinely edit lists concurrently; append-only with union merge handles the common case correctly. The shared list will stress the failure model in real conditions, which is what's needed.

---

## D5-pairing — Pairing UX

**Status:** Locked
**Decision:** Three mechanisms in v1.0; two deferred with documentation

**v1.0 mechanisms:**
- **A. QR code (primary)** — camera-based, works phone↔laptop and screen↔screen
- **B. Short numeric code (typed in)** — for when QR fails (lighting, broken camera, screen-to-screen on one device)
- **C. Magic link (one-time URL)** — for distance pairing (work laptop while home devices are at home); supports both internet-mediated and local-network-only modes

**v1.x deferred (documented, not built):**
- **D. BLE proximity** — best UX when devices are physically near; limited by browser BLE API support
- **E. NFC tap** — smallest UX surface; limited to phones with NFC + browser NFC API

**Rationale for shipping all three in v1.0:**
- Three real user scenarios, each with a natural primary mechanism
- All three share the same underlying pairing protocol; the cost of supporting three over one is small
- Power-user-plus-family audience needs all three (family member may not know how to scan QR; power user wants the magic link)

**Failure-model review:** All three mechanisms must work offline-local where topology allows. Two devices on same Wi-Fi must be able to pair without any internet. QR and code naturally support this; magic link needs to support a local-network variant where the URL points at the source device's local IP. Spec must include this.

---

## D9 — Mesh-layer base

**Status:** Locked
**Decision:** Wrap NetBird

**Rationale:**
- **Ethos match.** NetBird is BSD-3 open-source all the way down, including clients. No proprietary GUI pieces. Matches the rest of the stack's "open all the way down" position.
- **Identity integration is cleaner.** NetBird's OIDC integration model lets the fabric become the identity provider for the mesh.
- **Designed for self-hosting from day one** rather than being a self-hostable port of a centralized design.
- Headscale relies on Tailscale clients, which have proprietary GUI on macOS/iOS — contradicts the stack's ethos.

**Alternatives considered:**
- **Headscale** — rejected for proprietary client GUI on Apple platforms (real risk to brand coherence)
- **Build from raw WireGuard** — rejected as years of work re-solving solved problems

**Failure-model review:** ✓ Mostly holds. NetBird's coordination plane is the one component that needs to be reachable for new peers to join the mesh; once joined, peers can talk directly even if coordination is down. The wrapper must clearly document and surface "coordination plane offline" as a known state, with existing peers continuing to work over existing connections.

---

## D10 — Anchor image format

**Status:** Locked
**Decision:** curl|bash install script in v1.0; signed packages and VM image deferred to v1.2

**v1.0:**
- One-line install: `curl https://naklitechie.com/private-mesh | sh`
- Script detects platform, downloads binaries, lays down launchd plists (macOS) or systemd units (Linux), starts services
- User keeps existing OS install
- `--dry-run` flag for inspection before running
- `--uninstall` flag for clean removal
- Versioned, signed (gpg signature alongside binary)

**v1.2 (deferred):**
- Signed native packages (.pkg, .deb, .rpm) — proper OS-native installation, auto-update via standard mechanisms
- Tart/Lima VM image alternative — pre-built VM image for users who want full isolation

**Rejected entirely:**
- Custom OS image / bootable installer — wrong abstraction; anchor is not dedicated hardware in practice
- Docker-only deployment — requires Docker as user-visible dependency, contradicts stack ethos

**Failure-model review:** ✓ Holds. The installer must work fully offline once binaries are downloaded — no runtime calls home for license checks or telemetry. Already implied by "no telemetry" non-negotiable.

---

## D11 — Local inference engine choice

**Status:** Locked
**Decision:** Routing layer over MLX + llama.cpp

**Rationale:**
- Vision doc made a real bet on x86 anchors (Strix Halo, DGX Spark, Framework). Apple-only would retroactively break that bet.
- Apple Silicon reference platform: MLX is meaningfully faster (1.3-2x) than llama.cpp for supported models. Forcing llama.cpp on the reference platform leaves performance on the table.
- Routing layer is small (200-400 lines of Go) compared to building either integration alone.
- Bhai's existing MLX porting work feeds directly into the anchor's high-performance path.
- Forward compatibility: new engines (vLLM, exo, etc.) can be added to the routing layer.

**Concretely:** `nakli-llm-server` exposes one OpenAI-compatible API. Internally, a routing layer picks engine per model (MLX for supported models on Apple; llama.cpp for the long tail and non-Apple platforms).

**Failure-model review:** ✓ Holds. Inference is inherently local; the routing layer never needs network for local engines. Remote BYOK fails under network outage, but that's expected. The LLM primitive must clearly indicate "remote provider unreachable" so tools can degrade (offer to queue, suggest local fallback).

---

## D-FIF — Distributed FIF / Shamir recovery

**Status:** Deferred to v1.x; hooks present in v1.0
**Decision:** Ship v1.0 with `passphrase-only` envelope; design FIF format so v1.x can add distributed envelope types without changing the inner format

**v1.0 backup model:**
- FIF is one encrypted file, kept on multiple devices
- User responsible for keeping copies on all paired devices plus optionally cloud backup (iCloud Drive, Dropbox — the file is encrypted, cloud trust level doesn't matter for confidentiality), USB stick, etc.
- FIF is small (kilobytes) — having 5 copies is no burden

**v1.x options (architecture supports, not built yet):**
- `shamir-shares` — Shamir's Secret Sharing, K of N
- `device-quorum` — any K of N paired devices can collectively unlock
- `social-recovery` — any K of N trusted contacts collectively unlock

**Rationale for deferral:**
- No users yet; building elaborate recovery for a userbase that doesn't exist is premature
- Multi-device FIF copies cover most realistic loss scenarios
- Real loss events from real users will teach more about what recovery should look like than design speculation
- Adding it later doesn't break anything because envelope is layered

**Failure-model review:** ✓ Aligned. The layered FIF envelope is itself a failure-model decision — it ensures sovereignty doesn't preclude resilience. Hooks present from v1.0; full feature shippable in v1.x without protocol churn.

---

## D-Failure — Failure model as load-bearing

**Status:** Locked (new in v0.3)
**Decision:** The system is correct under continuous, asynchronous, partial failure, and recovers without intervention when connectivity returns

**Six v1.0 hooks:**

1. **Operation queue** — every primitive crossing a network boundary has a local queue with on-disk durability
2. **Causal ordering metadata** — every event carries vector clocks or Lamport timestamps + device IDs
3. **Bounded staleness visibility** — every Vault query returns data plus a freshness indicator
4. **Idempotency keys on Bridge calls** — Bridge calls with side effects carry idempotency keys for safe retry
5. **Graceful degradation surface** — SDK exposes "what's currently broken" to tools
6. **Conflict surface** — Sync emits conflict events when two peers wrote in the same causal slot

**Queue visibility:**
- Default: complexity is hidden. Tools just work; pending operations resolve invisibly.
- Power-user mode: fabric admin UI exposes the operation queue, pending operations can be inspected/retried/cancelled.
- This matches the dual audience: power users will drag in family members. Default serves the family; depth serves the power user.

**Rationale:**
- The internet was designed for failure as normal. SaaS un-designed that for billing reasons. The private mesh restores the original property.
- Power cuts, ISP outages, the anchor rebooting, devices on cellular with intermittent signal — all are normal operating conditions, not error conditions.
- Stronger than "support offline use" — governs every design decision.

**Implications across all primitives:** Every primitive that involves talking to anything beyond the current device must be local-first, idempotent, order-tolerant, resumable, symmetric, eventual.

---

## D-Queue — Queue visibility to users

**Status:** Locked (new in v0.3)
**Decision:** Hidden by default, visible on demand

**Rationale:**
- Power users want visibility into the system's state — part of sovereignty is seeing what's happening
- Family members dragged into the stack by power users want invisibility — defaults should serve them
- Both audiences are first-class; the design accommodates both via opt-in visibility

**Implementation:**
- Tools render the default user experience without exposing queue/sync/freshness details
- A fabric admin UI (accessible via a known path or settings menu) exposes the operation queue, pending operations, sync status, freshness per peer, error states
- Tools can opt into showing badges or indicators (e.g., a small "syncing" hint) when they want, but the default is silent operation

---

## D-Origin — Origin strategy and multi-origin guarantee

**Status:** Locked (v0.5)
**Decision:** First-party tools use per-tool subdomains; protocol is origin-neutral by design

**Four-point clarification:**

1. **First-party tools use per-tool subdomains.** Phase 1 shared list lives at `list.naklitechie.com`. Future first-party tools follow the same pattern: `tool.naklitechie.com`. Matches existing portfolio convention; storage isolation per tool is natural.

2. **Tools at any origin work identically.** `file://`, third-party domains, forks, self-hosted intranet, anywhere. The fabric does not depend on a specific origin. Mix-and-match across origins is a first-class property.

3. **Transports MUST accept requests from any origin** and authorize via capability tokens, not Origin headers. The Hub, Cloudflare Worker, and Local Network transports all return permissive CORS headers (`Access-Control-Allow-Origin: *` or echo-back). Origin checking is not an authorization mechanism in this stack; macaroons are.

4. **Tools MUST treat browser storage as cache, not authoritative.** Vault (via transport) is the source of truth. Storage at a new origin starts empty; the tool refreshes from Vault on first use. This is required by the failure model anyway (D-Failure) since storage at any origin can be cleared or unavailable.

**Rationale:**
- Per-tool origin matches existing portfolio convention (zero churn for future adoption)
- Storage isolation per tool is correct — cross-tool data sharing goes through fabric Grants, not incidental browser storage
- Origin-neutral protocol is required for the "mix and match" property the architecture promised
- All four points are already implied by D2 (FIF), D5 (transport), and D-Failure — making them explicit prevents future drift

**Failure-model review:** ✓ Aligns. Storage-as-cache is the failure-model-correct posture regardless of origin.

---

## D-Consumers — The fabric serves consumers of any shape

**Status:** Locked (v0.5)
**Decision:** Browser-served HTML is one consumer shape. Native binaries, daemons, CLIs, scripts, API callers, agents, and devices are equally first-class.

**Five-point commitment:**

1. **The fabric serves consumers of any shape.** A consumer is any process that speaks the Fabric Protocol — a browser tool with a UI, a native macOS app, a Go daemon, a Python script, an LLM agent with grants, an embedded device. The fabric does not privilege browser consumers; the portfolio happens to be mostly browser tools because that's the portfolio's preferred surface.

2. **The Fabric Protocol is language-neutral.** HTTP/JSON wire format. Documented capability token format (macaroons). Documented FIF format (layered envelope with binary inner). Any language with HTTP + JSON + standard crypto primitives can implement a client.

3. **Reference SDKs in JavaScript and Go from v1.0.** `fabric-sdk-js` for browser tools (the portfolio's dominant shape). `fabric-sdk-go` for binaries, daemons, the Hub itself, and the reference CLI. Both implement the same protocol; conformance tests verify equivalence.

4. **Phase 1 ships at least one non-browser reference consumer.** The reference CLI (`nakli` or similar — naming TBD) consumes the fabric for command-line operations. Read the shared list. Append items. Inspect the operation queue. Trigger sync. Manage grants. The CLI doubles as the operator surface for power-user features (D-Queue's "visible on demand" mode is largely realized through the CLI).

5. **The SDK is a convenience; the protocol is the contract.** Documentation positions it that way. Anyone can write a fourth SDK (Swift, Rust, Python, Kotlin, etc.) by implementing the protocol against the conformance test suite. Forks and third-party SDKs are normal, not exceptional.

**Terminology distinction** (matters for the spec):
- **Fabric Protocol** — wire format, language-neutral, the spec
- **Fabric SDK** — language-specific library that implements the protocol
- **Consumer** — any process that uses the protocol (via SDK or directly)
- **Tool** — a consumer with a user interface (usually a human user; could be an agent)

A tool is one kind of consumer. Spec language uses "consumer" for the broader category.

**Rationale:**
- Principle 3 (Primitives are interface-agnostic) is structural, not aspirational
- The Go Hub and Go CLI sharing an SDK is efficient — one language, one set of tests, one set of crypto routines
- A reference CLI in Phase 1 forces the protocol to be truly language-neutral from day one (browser-only protocols accumulate browser-isms that are hard to retroactively remove)
- The CLI also naturally serves the dual-audience design (D-Queue): power users use it; family members never see it

**Failure-model review:** ✓ The CLI is local-first by definition (it runs on the operator's own device), uses the same Sync semantics as browser tools, surfaces queue/freshness state directly (which is its point).

---

## D-Agents — Agents as principals, with derived authority

**Status:** Locked (v0.7)
**Decision:** Agents are first-class consumers AND principals with their own identity; they hold Grants from humans (or delegated from other agents under human-rooted chains); cryptographic attenuation is the security boundary

The portfolio was built for humans; the agent era is upending that. Without changing the architecture, several properties become load-bearing under agent use and must be locked explicitly.

**Seven-point commitment:**

1. **Agents have their own keypairs.** Distinct from device subkeys, distinct from user identity. When an agent acts, it acts as itself, holding Grants delegated from a human. The fabric records both the agent's signature and the Grant chain.

2. **Agent provisioning is a documented, vendor-neutral flow.** Mint an agent identity, scope its Grants, optionally set an expiry on the agent itself (not just on individual Grants). Swapping agent vendors should not require any fabric-level change.

3. **Macaroon attenuation is the security boundary, enforced server-side at every transport.** An agent cannot mint a Grant of strictly greater scope than the Grant it currently holds. Transports check this on every request; client-side enforcement is not sufficient. The conformance test suite includes adversarial cases — agent trying to use a Grant outside its namespace, agent trying to use an expired Grant, agent trying to delegate beyond its caveats.

4. **Bridge grants for side-effect operations can carry approval caveats.** Grants for Bridge calls that send email, transfer money, post publicly, modify external systems support macaroon caveats including `requires-human-approval`, `max-N-per-window`, `only-to-domain`, `max-amount`. The fabric expresses these natively; transport implementations enforce them; the provisioning UX makes them legible at minting time.

5. **All agent operations carry idempotency keys regardless of operation type.** Hook 4 (idempotency on Bridge calls) is upgraded under agent use: every operation an agent performs, including Vault writes, carries an idempotency key. A prompt-injected agent retrying "append item" 50 times produces one item, not 50.

6. **All agent operations log to History with full provenance chain.** Every entry identifies the agent's keypair, the Grant under which it acted, the Grant's parent, and so on to the human-issued root. Reads matter for audit, not just writes. The History query interface supports "show me all operations by agent X in window W" as a first-class query.

7. **Agent retirement is a first-class operation.** Revoke the agent's identity. The retirement event is appended to History. All Grants minted by or under that agent become unverifiable from the retirement timestamp forward. Existing History entries from before retirement remain verifiable.

**What the fabric does not solve:**

- **Prompt injection** is an agent-level concern. The fabric cannot prevent an agent from being manipulated by adversarial inputs. What the fabric does is bound the consequences: tight Grant scope, approval caveats on side-effect operations, History audit, anomaly detection (v1.x).
- **Anomaly detection** is a v1.x feature, not v1.0. The architecture supports it (History is queryable, Grants are scoped, events carry provenance). The actual detection logic — pattern recognition, threshold tuning, alert UX — comes later.
- **Vendor lock-in defense** requires more than the fabric. The fabric stays open and vendor-neutral; the broader ecosystem's resilience depends on continued availability of open-source and self-hostable agent options.

**Rationale:**
- The architecture survives the agent era without structural change. The seven primitives are correct. The transport plane is correct. The failure model is correct.
- But several properties that were "good hygiene" become "load-bearing security" under agent use. This decision locks them as non-negotiable.
- The macaroon-based Grant model was designed by Google researchers partly with delegated, attenuated, time-limited authorization in mind. It maps onto the agent-era requirements directly. We chose well in D1.
- The CLI commitment (D-Consumers) is now load-bearing for a different reason than originally framed: it forces the protocol to be fully documented and structured, which is what agents need.

**Failure-model review:** ✓ Aligned. Agent operations queue and retry under network failure like any other operation (Hook 1). Provenance metadata travels with events (Hook 2). Grant freshness is bounded and visible (Hook 3). Idempotency is now mandatory not optional (Hook 4). Agent operation failures surface in the degradation interface (Hook 5). Conflicts between concurrent agent operations are detectable (Hook 6).

---

## D-Brand — Launch face and branding

**Status:** Locked (v0.5)
**Decision:** NakliTechie umbrella with the stack as a named sub-product (working name "Private Mesh"); tools lead the marketing; the stack stays mostly quiet

**Shape:**
- **NakliTechie remains the studio brand.** The portfolio's accumulated equity (worldview, aesthetic, stance against SaaS) stays.
- **The portfolio remains "NakliTechie tools."** Individual tool branding (Tijori, Bahi, the shared list, etc.) doesn't change.
- **The Private Mesh is the architectural layer.** Working name; locks closer to launch per the naming standing rule. Tools that consume the fabric are described as "built on NakliTechie Private Mesh" — a feature, not the primary pitch.
- **Users can opt in by setting up the anchor**, or use individual tools standalone without ever knowing the stack exists.
- **The Karpathy-style blog post** (public articulation of the platform thesis) is where the stack gets its moment. Day-to-day, tools lead.

**Rationale:**
- NakliTechie has equity worth compounding; new brand resets to zero
- The architectural layer is real enough to deserve a name (saying "Private Mesh" is more compelling than describing it each time)
- But platforms don't sell themselves to users — tools do; the platform sells the tools, not vice versa
- This shape allows the Private Mesh brand to graduate to more prominence later if momentum develops, or stay quiet infrastructure if not — no rework needed either way

**Alternatives considered:**
- **Single new brand** (rejected) — throws away accumulated equity for the unknown gain of a new identity
- **No stack brand at all** (rejected as too narrow) — the stack thesis benefits from a name when articulated; no name makes the public writeup harder

**Failure-model review:** N/A — brand decision, not architectural.

---

## D-Monetization — Managed Relay tier as the monetization path

**Status:** Locked (v0.5) — shape locked; specifics deferred to Phase 4
**Decision:** Optional managed Relay tier as the monetization path; managed tier is convenience, not capability

**Shape (locked now):**

- **Managed tier is convenience, not capability.** Anything the managed tier can do, the self-hosted setup can also do. The tier sells deployment ease, operational reliability, and support — not features unavailable otherwise.
- **What the managed tier likely provides:** NakliTechie-operated Cloudflare Worker tier; one-tap setup vs the user deploying their own Worker; operational SLA, automatic updates, monitoring; same protocol, same crypto, same FIF.
- **What the managed tier does NOT do:**
  - Hold the user's keys or plaintext (impossible by design — end-to-end encryption is non-negotiable per principle 6)
  - Require an account that gates access to the user's own data
  - Provide features unavailable in the self-hosted version
  - Lock the user in — they can move to self-hosted at any time, FIF and Vault contents portable
- **Principle 10 applies to monetization:** every monetized feature must serve sovereignty, resilience, or both — or it doesn't ship. Convenience-only features earn their place only when they don't compromise sovereignty.

**Deferred to Phase 4:**
- Pricing
- Billing infrastructure (probably Stripe, but not committed)
- Exact onboarding flow
- Whether to offer a free tier, and at what threshold
- Whether to offer team/multi-user managed tiers
- Whether the managed tier uses pooled NakliTechie infrastructure or BYO storage (R2, S3, etc.)

**Rationale:**
- Monetization decisions made before users exist tend to be wrong; specifics need contact with reality
- But the *shape* of monetization needs to be committed now — to prevent future drift toward incompatible shapes (e.g. "tools start requiring accounts," which would betray the entire stack)
- The "managed tier as convenience" framing is the version of monetization that's consistent with every other decision in this document
- This shape allows the managed tier to be skipped entirely without breaking anything if it turns out users don't want it

**Failure-model review:** ✓ Aligns. The managed tier is one transport implementation among several; if it goes down, users with multiple configured transports (or those who can spin up their own Worker quickly) keep working. Per principle 8 (anchor is a peer, not center) extended: no single transport is a center.

---

---


## Open decisions

Only one remains. The rest of the originally-open decisions resolved cleanly in v0.5.

### O2 — Collaborator shaping

**Status:** Open by design — resolves after `fabric-spec-001-v1.0.md` exists
**Question:** What bounded modules does son own? What does brother own?

**Approach:**
- The spec will define natural module boundaries (transports, SDKs, primitives, consumers, mesh wrapper, conformance tests)
- Once boundaries are real, a short conversation with Bhai + son + brother assigns ownership
- Module interfaces are defined by the spec, so collaborators can work in parallel without coordination overhead

**Plausible module units** (illustrative, not committing):
- A specific transport implementation (Cloudflare Worker, Local Network discovery, the Go Hub)
- A specific SDK (the Go SDK; the CLI on top of it)
- A specific primitive end-to-end (Identity + FIF; Bridge + adapters; LLM + routing)
- A reference consumer (the shared list tool; the CLI)
- The mesh wrapper as its own sub-project
- Conformance test suites for the protocol

**Not pending:** Anyone's input right now. Correctly waiting for the spec.

---

## Decision history

| Date | Decision | Status |
|------|----------|--------|
| Early | D1 — Macaroons | Locked |
| Early | D2 — FIF cross-origin model | Locked |
| Early | D3 — Tools own merge | Locked |
| Early | D4 — Per-grant revocation | Locked → Revised v0.3 |
| Early | D5 — Transport plurality (2 impls) | Locked → Revised v0.3 (3 impls) |
| Early | D6 — Shared list | Locked |
| v0.2 | D5-pairing — QR+code+link | Locked |
| v0.2 | D9 — NetBird | Locked |
| v0.2 | D10 — curl|bash | Locked |
| v0.2 | D11 — MLX + llama.cpp routing | Locked |
| v0.3 | D-FIF — Distributed deferred, hooks now | Locked |
| v0.3 | D-Failure — Failure model load-bearing | Locked |
| v0.3 | D-Queue — Hidden by default, visible on demand | Locked |
| v0.5 | D-Origin — Per-tool subdomains, origin-neutral protocol | Locked |
| v0.5 | D-Consumers — Multi-client first-class, JS+Go SDKs in v1.0 | Locked |
| v0.5 | D-Brand — NakliTechie studio + Private Mesh sub-product | Locked |
| v0.5 | D-Monetization — Managed tier as convenience; specifics in Phase 4 | Locked |
| v0.6 | Coherence pass — vision unified into single HTML, all sections aligned | Locked |
| v0.7 | Principle 11 — All authority flows from a human-issued root | Locked |
| v0.7 | D-Agents — Agents as principals with derived authority | Locked |

---

## Re-examination notes (v0.3 failure-model pass)

Each prior decision was re-examined under the constraint "the system is correct under continuous, asynchronous, partial failure."

**No change required:**
- D1 (Macaroons) — verify offline ✓
- D2 (FIF) — local file works offline ✓ (also: layered envelope added)
- D3 (Tools own merge) — five CRDT-shaped properties made explicit ✓
- D6 (Shared list) — near-perfect failure-model stress tool ✓
- D9 (NetBird wrap) — coordination plane partition handled; document it ✓
- D10 (curl|bash) — offline-once-installed already implied ✓
- D11 (MLX + llama.cpp) — local inference is local; remote BYOK degrades gracefully ✓

**Refined:**
- D4 (Revocation) — refresh becomes opportunistic, not gating; session grants survive partition
- D5 (Transport plurality) — Local Network promoted from v1.1 to v1.0 (third reference implementation)
- D5-pairing — all three v1.0 mechanisms must work offline-local where topology allows

**New:**
- D-FIF — Layered envelope; v1.x distributed-FIF without protocol churn
- D-Failure — Failure model as load-bearing non-negotiable
- D-Queue — Default-hidden, on-demand-visible operation queue
