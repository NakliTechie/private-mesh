# NakliTechie Private Mesh — Vision

**Working names (all TBD):**
- The overall project — Private Mesh (NakliTechie Private Mesh in full)
- The fabric (capability layer) — TBD (candidates: Tanaab, Saath, Tether, Weave, Lattice)
- The Hub (relay binary) — TBD (likely `nakli-hub`)
- The network mesh (network plane, NetBird-wrapper) — TBD (candidates: Reshma, Strand, Filament, Loom)

**Document:** `private-mesh-vision-001-v0.7.md`
**Status:** Vision draft, agent-era pass complete.
**Audience:** Bhai + collaborators (son, brother) + future contributors.
**Supersedes:** `private-mesh-vision-001-v0.6.md` — adds Section "Agents as principals" (after Audience), Principle 11 (authority flows from a human-issued root), and language refinements to Bridge primitive and the audience model under agent use. Cross-references to D-Agents in the decisions log.

---

## The thesis

In the next few years, every serious computing setup — at home, at work, at small businesses, across distributed teams — will have always-on compute appliances anchoring it. Like routers and modems today. Like electrical boxes. Install and forget. Powerful enough to run real AI inference locally — LLMs, vision models, audio models — without sending a single byte to OpenAI, Anthropic, Google, or anyone else. Apple Silicon clusters, Framework desktops with Strix Halo, NVIDIA DGX Spark successors, AMD Ryzen AI Max boxes. The hardware is here; the software stack to make it usable for normal people, small teams, and small organizations isn't.

A **private mesh** is the architecture: a mesh of devices, people, and compute that belongs to one user or one trusted group, not to a vendor. The mesh can span a single home, a single office, multiple offices linked by VPN, a family across multiple houses, a small business with a few locations, a co-working space, or a distributed small team. The unit is "the people and devices that share trust," not "the building they're in."

Within a private mesh, an always-on compute anchor — typically a Mac mini at home, a workstation in an office, or a small Apple Silicon cluster somewhere — serves as the most-available peer. It runs inference. It holds the durable data. It brokers access. But it isn't the center; it's a peer, just the most reliably reachable one. Phones, laptops, tablets, friends-and-family devices, colleagues' devices all orbit around it as peers, syncing data, sharing capabilities, running locally-routed inference. When the anchor is down, the mesh degrades to peer-to-peer mode — devices on the same network still talk to each other, work still gets done, life continues.

The NakliTechie Private Mesh is the software layer that makes this work — end to end. Browser-native tools at the top. Capability-based data fabric in the middle. A self-hostable Hub binary on the anchor. A sovereign network mesh (NetBird-wrapped) for routing between sites. Observability across the fleet. All open, all owned by the user or the group, no accounts on any third party that isn't pure infrastructure (Cloudflare-as-CDN, the user's ISP, similar).

This is not seven small tools that happen to share aesthetics. This is **a platform for sovereign personal and small-group computing**, with always-on compute anchors as its physical foundation.

---

## The failure model is load-bearing

The internet was designed to route around failures. Power cuts, network partitions, dropped packets — the protocol assumes failure is normal. Then B2B SaaS centralized everything because central servers were easier to reason about for vendors who wanted to bill per seat. The user got reliable-when-everything-works and broken-when-anything-doesn't. The private mesh rejects this.

**The system is correct under continuous, asynchronous, partial failure, and recovers without intervention when connectivity returns.** Power cuts, ISP outages, the anchor rebooting, a device on cellular with intermittent signal, two devices both offline for a week and coming back with different states — none of this is an error condition. It's Tuesday.

This is a stronger constraint than "support offline use." It governs every design decision. Re-examine any choice through this lens; if the choice assumes "the network is up" or "the anchor is reachable" or "the user can refresh tokens before they expire," the choice is probably wrong.

### What this constraint implies

Every primitive that involves talking to anything beyond the current device:

- Must be **local-first** — the local operation succeeds without network, and the network operation is layered on top.
- Must be **idempotent** — replaying the same operation is safe. Same event arriving twice is a no-op.
- Must be **order-tolerant** — operations can arrive out of order; the merge logic handles it.
- Must be **resumable** — interrupt at any point, restart later, no corruption.
- Must be **symmetric** — no privileged role; any peer can be source or destination.
- Must be **eventual** — given any pair of peers that can eventually reach each other, they converge to the same state.

These are CRDT-shaped properties at the protocol level. Tools may use the merge-helpers library for simpler semantics on top, but the underlying protocol always provides these guarantees.

### The six v1.0 hooks for the failure model

1. **Operation queue.** Every fabric primitive that crosses a network boundary has a local queue with on-disk durability. Tools call `fabric.sync.append(event)` and the SDK handles when the network can actually carry it out. A crashed device doesn't lose intent.

2. **Causal ordering metadata.** Every event carries a vector clock (or Lamport timestamps + device IDs at minimum). Receivers can determine causal relationships regardless of arrival order. Tools that need it use it; tools that don't can ignore it.

3. **Bounded staleness visibility.** Every Vault query returns data plus a freshness indicator — when did this device last sync with each peer? Tools choose what to show; the data is available to them.

4. **Idempotency keys on Bridge calls.** Bridge calls with side effects carry idempotency keys. External services that support deduplication (most modern APIs) use them. Retry is safe even after network failure mid-call. *Under agent use (see D-Agents), this becomes a security property: a prompt-injected agent retrying without idempotency could amplify a single intended action into many; with idempotency, the amplification fails.*

5. **Graceful degradation surface.** The SDK exposes "what's currently broken" to tools. "Anchor unreachable, last seen 12 minutes ago." "Revocation list is 3 hours stale." Tools choose how to render this; the fabric makes it available.

6. **Conflict surface.** When two peers both wrote in the same causal slot, the Sync primitive emits a conflict event. Tools listen and decide. Append-only tools ignore conflicts (they don't have them); tools with mutable state handle them.

### Queue visibility to users

Default behavior: complexity is hidden. The user opens a tool, the tool just works, pending operations resolve invisibly when the network supports them.

Power-user behavior: a fabric admin UI exposes the operation queue. "5 events pending sync to anchor. Last successful sync 2 hours ago." User can inspect, retry, cancel pending operations. This view is opt-in — users navigate to it deliberately.

This dual mode matches the realistic audience: early users will be power users who drag in family members. The power user wants visibility; the family member wants invisibility. Default hidden, accessible on demand.

---

## The layered architecture

```
+--------------------------------------------------------------------+
|  Consumers (any shape)                                             |
|  Browser tools (Tijori, Bahi, Slate, shared list, ...) ·           |
|  Native binaries · CLIs (nakli-cli) · Daemons · Agents · Devices   |
+--------------------------------------------------------------------+
|  Fabric SDK (JavaScript + Go reference implementations)            |
|  Identity · Grant · Vault · History · Sync · LLM · Bridge          |
+--------------------------------------------------------------------+
|  Fabric Protocol (HTTP/JSON wire format, language-neutral)         |
+--------------------------------------------------------------------+
|  Transport Reference Implementations                               |
|  Go Hub binary · Cloudflare Worker · Local Network (mDNS) · others |
+--------------------------------------------------------------------+
|  Network Plane (mesh VPN, optional but recommended)                |
|  NakliTechie-wrapped NetBird for sovereign mesh                    |
+--------------------------------------------------------------------+
|  Hardware: The Anchor (home box, office workstation, NAS, cluster) |
|  Apple Silicon cluster, Strix Halo desktop, DGX Spark, NUC, NAS,   |
|  or any always-on machine the user owns                            |
+--------------------------------------------------------------------+
|  Observability: Gleam                                              |
|  Fleet observability, cluster telemetry, RDMA-over-Thunderbolt 5   |
+--------------------------------------------------------------------+
```

Each layer is independently useful. Each layer is independently shippable. Each layer reinforces the others. The whole is dramatically more than the sum of its parts because they share an audience, an ethos, and a coherent design philosophy.

---

## Why this exists

### Problem 1: Sovereignty is structurally unwon
SaaS won the last decade by trading data sovereignty for convenience. The opt-out community (NakliTechie's audience) has bypassed this trade by accepting more friction — each tool re-rolling its own storage, auth, sync. The result is a portfolio of excellent islands and very few bridges. No platform value compounds. Every new tool starts from zero on infrastructure questions that should be solved once.

### Problem 2: The compute anchor has no native software stack
Apple, NVIDIA, AMD, Framework are shipping the hardware. The software stack assumes you'll use the box to run someone else's services — Ollama for inference, Plex for media, Home Assistant for automation, etc. There is no integrated stack that treats the anchor as the foundation of the user's (or group's) personal computing universe, with tools, data, and identity all flowing through it sovereignly.

### Problem 3: Mesh VPNs solve networking, not data
Tailscale, Headscale, NetBird, Nebula all solve "devices can reach each other." They do not solve "tools can share authenticated, authorized data with each other." That layer is missing. People build it ad hoc per project — file shares, S3 buckets, syncthing, hand-rolled APIs. The fabric is the missing data layer above the network plane.

### Problem 4: Local-first inference needs local-first data
The locally-run LLM is useless without locally-available data. RAG over your notes, fine-tuning on your patterns, agents acting in your context — none of this works if your data lives in SaaS silos. The private mesh makes the data layer match the inference layer: both local, both owned, both sovereign.

### Problem 5: B2B SaaS centralized the resilience the internet was designed for
The internet was designed for failure as normal. SaaS un-designed that for billing reasons. The private mesh restores the original property: continuous, asynchronous, partial failure is the operating condition, not the exception.

### Problem 6: AI assistants change what one person can ship
Three people with AI assistants can do what twenty did a decade ago. The constraint on ambitious sovereign computing projects has never been the design or the audience — it's been the engineering cost. That constraint has dropped by an order of magnitude. The opportunity is to build at the ambition level that matches the new cost structure.

---

## Audience

### Primary audience: the private-mesh operator
The person who sets up the mesh and brings others onto it. Has at least one always-on machine somewhere (home, office, co-located), or is willing to acquire one. Refuses SaaS by default. Accepts third-party infrastructure only as raw substrate. Multi-device. Comfortable with technology. Wants the data, the tools, the inference, and the network all under their control or their group's control.

These are the people who run the mesh. They might be:
- A technical individual running it for their own use
- A technical family member running it for their household
- A small-business owner or technical lead running it for an office of 3-25 people
- A distributed small team's designated infrastructure person
- A co-working space's resident technologist
- A homelab enthusiast extending their setup beyond their own use

### Secondary audience: the dragged-along participant
The people the operator brings onto the mesh — family members, colleagues, small-team participants. Not necessarily technical. Want things to just work. The mesh must be usable by them — defaults hide complexity, the primary device is "the thing my brother / colleague / sysadmin set up," failure modes are visible to the operator (not them).

### Tertiary audience: the tool author
Wants to ship sovereign, browser-native tools without re-rolling sync, auth, storage, AI integration each time. Builds on the fabric SDK, inherits multi-device + capability enforcement + LLM access for free. Spec-first methodology fits naturally.

### Quaternary audience: the compute enthusiast and small-business operator
The Apple Silicon cluster homelab person, the Exo user, the Strix Halo early adopter, the small business looking at on-prem AI inference. Have the hardware (or are about to acquire it); lack the integrated software stack.

### Who this is not for
- Users who want SaaS convenience without sovereignty trade-offs
- Tool authors building outside append-only event-sourced data models
- Users who refuse all third-party infrastructure (the fabric supports them via sneakernet, but they are not the target)
- Anyone wanting a general-purpose enterprise capability platform with broad surface area
- Large organizations needing audit/compliance/governance tooling (not this stack's scope)

---

## Agents as principals

The portfolio was built for humans. KanZen, BOFH, Bahi, Stance, Mehfil — all assume a human at the keyboard. The thinking, the choice-making, the voice — those were the point. Tools gave humans leverage on their own intent.

That assumption is collapsing. Agents — LLM-driven, capable of multi-step reasoning, equipped with tools and credentials — are becoming the primary mode of use for a growing share of computing. Not as occasional power-user surfaces, not as scripted automation. As principals in their own right, acting on behalf of humans who delegate to them and review at decreasing frequency.

By the time the Private Mesh ships v1.0, a meaningful share of Bahi entries will be created by an agent acting under a Grant, not by a human typing. By v2.0, the ratio may have flipped for many users. This is not a future scenario being speculated about. It is the operating reality the platform must work in.

> The Private Mesh wasn't designed for either scenario specifically — it was designed for sovereign computing. Sovereignty matters in both. Possibly more in the agent-heavy one.

### What survives, what shifts, what becomes essential

Walking the existing portfolio through the agent-era lens, honestly:

**Tools that survive cleanly.** Tools where the agent doing the work is the point in the new world. Bahi (agent does the bookkeeping, human reviews). VaultMind (agent reads the notes, surfaces what's relevant). Tijori (agent fetches the right credential at the right moment). Kagaz (agent generates the site from a brief). These don't break — they get more valuable because agents can do the heavy lifting. The fabric makes this safe.

**Tools that get philosophically strange.** Tools where the human voice is much of the value. BOFH only works if the human writes the email; an agent writing BOFH replies misses the point. KanZen organizes knowledge — but whose knowledge, organized by whom? Bolo is for a human speaking; agent speech is a different category entirely. Stance anchors positions — if an agent takes a position, whose position is it? These tools don't break technically; they break philosophically. The "what is this for in the agent era" question is a per-tool conversation, not a Private Mesh decision.

**Tools that become essential.** Tools whose value scales with agent use. Stance becomes more important when agents are acting on your behalf and you need a tamper-evident record of *your* positions versus *theirs*. Mahalla becomes essential when "the agent represents you in the community" demands verifiable identity. Mehfil becomes vital when humans-in-rooms-with-other-humans is the rare thing.

### Architectural commitments for the agent era

The full operational consequences live in D-Agents. The shape:

- **Agents are consumers (per D-Consumers) but also principals with their own identity.** An agent has its own keypair, minted at provisioning. Acts as itself, holding Grants from a human.
- **All capability flows downstream from a human-issued root Grant** (Principle 11). Macaroon attenuation is the security boundary, enforced server-side at every transport.
- **Bridge calls with side effects are higher-stakes** for agents than Vault reads. Grants can carry caveats: `requires-human-approval`, `max-N-per-window`, `only-to-domain`, `max-amount`. Macaroon caveats express these natively.
- **All agent operations carry idempotency keys.** Hook 4 upgraded — every agent operation, not just Bridge calls. A prompt-injected agent retrying "append item" 50 times produces one item, not 50.
- **All agent actions are in History with full provenance.** Reads matter for audit, not just writes. The chain is traceable to the human-issued root Grant.
- **Agent retirement is a first-class operation.** Revoke the agent's identity. History records the retirement. All Grants minted by or under that agent become unverifiable from that point.

### What the fabric does not solve

- **Prompt injection** is mostly not the fabric's problem. The fabric cannot prevent an agent from being manipulated by adversarial inputs. What the fabric does is bound the consequences: tight Grant scope, approval caveats on side-effect operations, History audit, anomaly detection (v1.x).
- **Vendor lock-in defense** via agent provisioning requires more than the fabric. The fabric stays open and vendor-neutral. Today you use Claude Code; tomorrow you swap to an open-source agent; the fabric does not notice. The broader ecosystem's resilience depends on continued availability of open-source and self-hostable agent options.

### The audience model, revised

The dual-audience design (operator + participant) survives, but a third dimension emerges:

- **The operator** still sets up the mesh, mints agents, scopes their Grants, sets up anomaly thresholds.
- **The participant** still uses tools — and now has agents acting on their behalf, with their own consent and configuration.
- **The agents themselves** are consumers without eyes. The "visible to the curious" UX is irrelevant to them. They need a structured operations surface: documented endpoints, structured errors, predictable JSON shapes. The CLI is the human-readable view of this surface; agents talk to the same endpoints directly.

A family member's grocery list shouldn't surprise them with agent-added items unless they configured an agent to do that. The dragged-along participant's relationship to "their" agents must be intentional, not inherited from the operator's choices.

---

## The seven primitives

Build order: Identity → Grant → Vault → LLM → History → Sync → Bridge. Sync is treated as its own primitive (rather than part of Vault) because it's substantial enough to deserve its own surface area.

### Identity
Cryptographic identity for users and devices. Passphrase-derived root keys. Per-device subkeys, enrolled via pairing. The fabric never sees the passphrase; only key material derived from it touches the wire, always for verification rather than recovery.

Identity is portable across origins via the **Fabric Identity File (FIF)** — an encrypted bundle the user holds on disk. Accessible to tools at any origin, decrypted with the passphrase. Works offline (the FIF is a local file).

**Layered envelope design.** The FIF format is two layers:
- **Outer layer (envelope):** declares how to derive the decryption key. v1.0 ships one envelope type: `passphrase-only` — salt, KDF parameters (Argon2id), authentication tag.
- **Inner layer (identity material):** keys, configured transports, granted capabilities, optional recent-state cache. Format is independent of the envelope type.

In v1.x, new envelope types can be added without changing the inner format:
- `shamir-shares` — any K of N Shamir's Secret Sharing shares reconstruct the key
- `device-quorum` — any K of N paired devices can collectively unlock
- `social-recovery` — any K of N trusted contacts can collectively unlock

A v1.0 FIF and a v1.x FIF have identical inner content; they differ only in how the decryption key is obtained. Older readers can refuse newer envelope types gracefully. FIF rotation (re-encrypt with a new envelope type) is a first-class fabric operation.

No escrow. No NakliTechie recovery service. Loss of all FIF copies + loss of all paths to reconstruct the key = permanent loss. The user bears the responsibility; the fabric provides the architecture for the user to choose their own resilience model.

### Grant
Capability tokens implementing the macaroon model. A Grant says: "this holder is authorized to do this thing in this scope, subject to these caveats, until this time." Grants can be delegated, attenuated, revoked.

**Two revocation modes**, selected per-grant via caveat:

- **Opportunistic refresh** — for grants the user holds on their own devices. Long-lived expiry (days or weeks). Holder refreshes opportunistically when network is available; under partition, the existing grant keeps working. Loss-of-network does not lock the user out of their own data.

- **Revocation list** — for delegated grants to other parties. Longer expiry, list-backed revocation. Vault checks the list at use time. List staleness is bounded and visible (Hook 3). Immediate revocation when list propagates; stale-list grace period accepted as a property of the system.

The revocation-list staleness is the explicit trade-off: under network partition, a grant revoked 30 seconds ago might work on a peer that hasn't synced the list yet. This is acceptable because the staleness is bounded and visible, and because immediate revocation under partition is not actually achievable by any system that respects the failure model.

### Vault
Encrypted, content-addressed event storage. Append-only per-device event streams, signed by the appending device's key, encrypted with keys derived from Identity. Namespaced per tool. Bahi cannot read Tijori's namespace even though both run on the same fabric.

Vault stores blobs. Tools decide what blobs mean. All writes are local-first; reads return what's locally available with bounded-staleness metadata.

### History
Append-only hash-chained log. Generalizes the pattern already used in Bahi (audit log), Stance (anchored positions), and Tijori (event chain). Used for: audit, revocation lists, conflict resolution metadata, anything where order and tamper-evidence matter.

History entries are Vault events with extra structure. Each event in a History stream includes a hash of the previous event in the same stream; verification walks the chain. Different devices can extend their own chains independently when offline; when reconnected, peers see each other's appends and merge by union.

### Sync
Ordered event delivery between authorized peers, providing the five CRDT-shaped properties (idempotent, order-tolerant, resumable, symmetric, eventual).

The fabric guarantees:
- Every authorized peer eventually sees every event
- Events are delivered in a partial order that respects causal dependencies
- Concurrent events (same causal ancestor, different devices) are detectable by tools via the conflict surface (Hook 6)

The fabric does NOT define:
- What events mean
- How concurrent events combine into state (beyond ordering metadata)
- Whether duplicate or replayed events are errors or idempotent at the application level

A separate `fabric-merge-helpers` library provides common merge patterns (append-union, last-write-wins-per-key) for tools that don't need custom semantics.

### LLM
A standard interface for AI access. BYOK keys for remote providers never leave the user's environment beyond what the provider necessarily sees. Per-tool grants control which tools can invoke which providers with which budgets.

**Routing order (local-first):**
1. Local inference on the anchor when reachable (highest priority)
2. Local browser inference via Transformers.js, wllama, or MLX-WASM for tasks that fit
3. Remote BYOK as last resort

Tools express needs at the capability level ("fast text completion", "vision model", "32k context"); the LLM primitive routes to the cheapest+best+most-sovereign option that satisfies. Routing rules are user-controlled.

`nakli-llm-server` runs on the anchor and exposes an OpenAI-compatible HTTP API. Under the hood, a thin routing layer picks between MLX (Apple Silicon, best performance for supported models) and llama.cpp (everywhere, broader model coverage). The routing layer is small but real — it's how the private mesh stays hardware-diverse without sacrificing performance.

When remote BYOK is unreachable, the LLM primitive surfaces this clearly so tools can degrade (offer to queue, suggest a local fallback, etc.). LLM operations participate in the operation queue (Hook 1).

### Bridge
External service integration. Third-party APIs accessed with user-provided credentials, brokered through Grant.

Every Bridge call carries a Grant; the Grant gates which tool can use which credentials for what purpose with what budget. Credentials never persist in fabric-owned storage — they live in the FIF (encrypted at rest), decrypted in memory per session.

Bridge calls with side effects (post a calendar event, mint a transaction, send an email) carry idempotency keys (Hook 4). Retry after network failure is safe. Bridge ships with reference adapters for common services; new adapters are documented and accepted from contributors.

**Under agent use** (see "Agents as principals" and D-Agents), Bridge grants are higher-stakes than Vault grants. A Vault grant lets the agent read; a Bridge grant lets the agent act on the world. Bridge grants for side-effect operations support macaroon caveats including `requires-human-approval` (the operation queues for explicit human confirmation before execution), `max-N-per-window` (rate limit), `only-to-domain` (allowed call destinations), `max-amount` (for financial operations). The fabric expresses these natively; transport implementations enforce them server-side; the provisioning UX surfaces them at the moment the human is minting the Grant.

---

## The transport plane

The Transport Protocol is the spec. Multiple reference implementations ship in v1.0:

### nakli-hub (Go binary)
Self-hostable transport. Runs on the anchor, on a VPS, on a Raspberry Pi, on whatever always-on machine the user has. Single binary, single config file, no external dependencies. Speaks HTTP/JSON. Stores ciphertext at rest. Authorizes via macaroon validation. Notifies subscribed peers of new events.

The default sovereign path — the user's own box, the user's own data, the user's own network.

### Cloudflare Worker reference implementation
Zero-ops alternative. ~50-200 lines of TypeScript, user-deployed, backed by R2 for blob storage and KV for state. For users without an anchor yet, or as a fallback path.

The "I want this to just work without setting up a server" path.

### Local Network (mDNS) transport
**Promoted to v1.0 status** (was previously v1.1).

Two devices on the same Wi-Fi sync directly without going through any external service. mDNS discovery finds peers; HTTPS over local IP carries the protocol. No anchor required. No Cloudflare. No internet at all.

This is the most failure-resilient mode (everything else can be down, this still works) and the most sovereign (no third party at any layer). For office scenarios like Mehfil-style group work, it's the right default. For households where everyone's on the home Wi-Fi, or offices where everyone's on the corporate LAN, it works even when the WAN is down.

Local Network is not a replacement for the others — peers still want to sync to the anchor for durability across reboots. But it's a first-class transport, used when applicable, alongside the others.

### Future transports (v1.x and beyond)
Anyone can write another implementation conformant to the spec:
- Vercel Edge + Vercel Blob
- Deno Deploy + Deno KV
- AWS Lambda + S3
- Plain Node.js + filesystem (for the user with an old Linux box collecting dust)
- Embedded in another tool (an Electron app could host the transport internally)

Conformance tests verify any implementation against the protocol spec. Multiple implementations can coexist in one user's setup — Cloudflare as always-available fallback, anchor Hub as preferred path, Local Network when devices are co-located.

---

## The network plane

The fabric assumes some network reachability for cross-device sync (Local Network transport handles same-network sync without internet). For remote access, three options:

### Option 1: User has a public endpoint
Hub runs behind the user's reverse proxy (Caddy, nginx, Traefik) with a real TLS certificate on a domain the user owns. Phone connects to `hub.user-domain.com` from anywhere. Standard internet plumbing.

### Option 2: NakliTechie mesh (NetBird wrapper)
A wrapped NetBird coordination plane, bundled with the anchor. WireGuard-based, peer-to-peer encrypted, NAT-traversal-handled. Phone joins the mesh; Hub is reachable at a stable mesh-internal address.

**Why NetBird, not Headscale.** NetBird is BSD-3 open-source all the way down, including clients. No proprietary GUI pieces anywhere. The all-open ethos matches the rest of the stack. NetBird's identity model integrates cleanly with fabric Identity. NetBird was designed for self-hosting from day one rather than being a self-hostable port of a centralized design.

The wrapper does NOT reinvent WireGuard or rewrite NetBird's components. It provides:
- One-tap pairing for new devices (QR from anchor, scan from phone, done)
- Fabric-aware identity integration (mesh identity derived from fabric Identity)
- Sensible defaults for the typical home or small-office use case
- Bundled distribution with the anchor installer

If the user already runs Tailscale, NetBird, or another mesh, they can use it instead — the fabric does not care which mesh is in use, only that the Hub is reachable.

When the NetBird coordination plane is unreachable, existing mesh peers continue to communicate (NetBird supports this). New peer enrollment fails until coordination is back. The wrapper exposes this state clearly.

### Option 3: User accepts cloud-mediated transport
Cloudflare Worker (or equivalent) acts as the public endpoint. No anchor required for reachability. Sovereignty story is slightly weaker (Cloudflare sees ciphertext and metadata) but acceptable for users who haven't committed to an anchor yet.

The fabric supports all three. Most users will end up using Option 2 once the anchor is real, with Local Network handling same-network sync and Cloudflare as fallback.

---

## Hardware: the anchor (home box, workstation, NAS)

The private mesh is hardware-agnostic but written assuming:

### Reference platform: Apple Silicon
- Mac mini, Mac Studio, MacBook running unattended (at home or office), or a small cluster connected via Thunderbolt 5
- macOS 26.2+ for RDMA over Thunderbolt 5 (real distributed inference becomes possible)
- MLX as the primary local inference engine for supported models
- Bhai's existing setup (M4 Pro + M4 Max Studio over SSH) is reference dev environment

### Secondary platform: x86 Linux
- Strix Halo desktops (Framework, ASUS, others)
- NVIDIA DGX Spark and successors
- Generic Linux box with a decent GPU or unified-memory APU
- llama.cpp as the local inference engine

### Tertiary platform: any always-on Linux box
- NAS (Synology, QNAP, custom)
- Raspberry Pi 5 or equivalent SBC
- VPS for users without dedicated hardware yet
- No local inference; the box is just the Hub + storage anchor

The private mesh scales down to "a Pi running Hub" and up to "an Apple Silicon cluster running cluster-distributed inference." The fabric and protocols are the same across the range; what changes is what the anchor can do beyond hosting the Hub.

---

## Observability: Gleam

Gleam is already in flight. It becomes a first-class layer of the private mesh rather than a standalone project. The anchor runs Gleam; the user's fleet (laptop, phone, additional machines) feeds Gleam telemetry; the user sees what their stack is doing.

Specifically, Gleam is where the private mesh becomes observable to the user:
- Hub throughput, latency, error rates
- Inference workload and queue depth on the anchor
- Sync activity per tool per device
- Mesh-layer connectivity status
- **Operation queue depth and age** (Hook 1 visibility)
- **Per-peer freshness** (Hook 3 visibility, aggregated across the fleet)

Gleam is the operations interface for the power-user mode. Not required to use the stack; the place power users go when they want to see what's happening or when something is off.

Gleam upstreams to last9 from a position of credibility once the private mesh is real.

---

## Tools layer

The browser-native tools — Tijori, Bahi, Slate, VaultMind, Stance, Mahalla, Mehfil, Bolo, the full portfolio — sit on top.

### Existing tools
Existing tools are doing fine and not migration candidates. Each tool's existing roadmap continues. Some will adopt the fabric SDK when it's stable, on the author's own timing. The fabric is offered, not imposed.

### Phase 1 conformance consumer: a shared list
The first fabric-native tool. A household shared list — groceries, todos, packing lists. One append-only stream of items; tick states are events. Multi-user: Bhai, son, brother all editing the same list daily.

This tool's job is to prove Phase 1 works under real conditions. Multi-user pressure from day one. Daily use is automatic for any household. Concurrent writes from different devices/users naturally happen every week — exactly the scenarios the failure model has to handle.

If it works well, it becomes a portfolio-worthy tool. "Family list that doesn't require everyone to sign up for an account or share an iCloud" is a real gap.

### Subsequent tools
After the shared list ships and the fabric stabilizes, new fabric-native tools follow at portfolio pace. Each one is now half the work it would have been pre-fabric because storage/sync/auth/LLM are inherited.

---

## What's in scope

- Full design and implementation of the seven primitives
- The Fabric Protocol spec, with three reference transport implementations in v1.0 (Go Hub, Cloudflare Worker, Local Network/mDNS)
- Reference Fabric SDKs in JavaScript and Go (v1.0)
- A reference non-browser consumer — the `nakli-cli` (Go) — as the operator surface for queue/freshness/grants
- The Fabric Identity File (FIF) format with layered envelope (passphrase-only in v1.0; hooks for v1.x envelope types)
- The six failure-model hooks as v1.0 requirements
- The NetBird-wrapped mesh layer (separate parallel project)
- The local inference daemon (`nakli-llm-server`) with MLX + llama.cpp routing
- A conformance test suite that any transport or SDK implementation must pass
- Documentation sufficient for an external tool author to build a fabric-consuming tool
- The shared list tool as Phase 1 conformance consumer
- Migration paths (optional, on tool author's timing) for existing portfolio tools
- Integration with Gleam for observability
- Anchor install script for Apple Silicon (and x86 Linux soon after)

## What's not in scope (now)

- Cross-organization capability delegation. Personal scale only.
- General-purpose pub-sub or message queuing.
- Server-side hosted tier as a default offering. (Optional managed Relay tier as a monetization path, distinctly secondary.)
- Mobile-native apps. Web/PWA first. Native wrappers possible later if PWA limits become real blockers.
- Account recovery via NakliTechie infrastructure. No escrow, no reset.
- Real-time collaborative editing via the fabric primitives directly. Tools may implement CRDTs on top of Sync, but the fabric does not provide live cursors / OT / RGA out of the box.
- Replacing the user's existing identity providers, password managers, or daily-driver apps that they're happy with.
- BLE-proximity pairing in v1.0 (deferred — see pairing section).
- NFC-tap pairing in v1.0 (deferred — see pairing section).
- Native macOS/Linux/Windows installers in v1.0 (curl|bash installer ships first; signed packages in v1.2).
- VM image distribution in v1.0 (Tart/Lima images in v1.2).
- Distributed FIF / Shamir / social recovery in v1.0 (hooks present from day one; full feature in v1.x).

---

## Guiding principles

The principles guide future decisions. When a new question comes up — a feature, a trade-off, a design choice — these are what to reach for first. They sit above the non-negotiables: principles are the source, non-negotiables are the consequence.

### 1. Failure resistance is structural, not optional.
Power cuts, network partitions, dropped peers, dead devices are operating conditions, not error conditions. Every primitive is local-first; every network operation is queued; every state is eventually consistent. The internet was designed for this; SaaS un-designed it for billing reasons; the private mesh restores it.

### 2. The home is online even when the internet isn't.
Devices on the same network can reach each other without going through anything external. Sync, inference, capability checks, pairing — all work between local peers when WAN is dead. Internet outage degrades the stack's reach, not its function.

### 3. Primitives are interface-agnostic.
Whatever's underneath must support HTML, native apps, CLIs, or anything else. HTML is the preferred surface for the portfolio; others may prefer native; both are first-class. The fabric speaks HTTP/JSON; any client that speaks HTTP/JSON is a valid consumer. Tools choose their own surface; the fabric doesn't care.

### 4. Sovereignty over convenience, but convenience is not the enemy.
The user controls the data, the keys, the infrastructure. But "sovereign" is not an excuse for friction. Where convenience can be delivered without compromising sovereignty — good defaults, smooth pairing, automatic transport selection, opt-in complexity — deliver it. Sovereignty without usability is dogma; usability without sovereignty is SaaS. The private mesh is both.

### 5. Visible to the curious, invisible to the unwilling.
Power users see queues, freshness, error states, internals. Family members see "open app, use app." Both audiences are first-class; the design accommodates both via opt-in depth, not by forcing complexity on the unwilling or hiding it from the willing.

### 6. Open all the way down.
No proprietary components in the critical path. The stack, the dependencies (NetBird BSD-3, WireGuard open), the protocols, the binaries are all auditable. "Open core with proprietary cloud" is the failure mode the stack exists to reject; the stack must not become that.

### 7. The user owns their failure modes.
No NakliTechie recovery service. No escrow. If you lose all your keys, your data is gone. The fabric provides the architecture for the user to choose their resilience (multi-device copies, Shamir, social recovery), but the choice is theirs and the consequence is theirs. Sovereignty includes the right to lock yourself out.

### 8. The anchor is a peer, not the center.
The anchor (home box, office workstation, NAS, cluster) is the most-available peer, the durability backstop, the inference workhorse. But the fabric works when the anchor is down. Phones still talk to phones; laptops still talk to laptops; data still syncs across paths the anchor wasn't on. The anchor being unavailable degrades the mesh's capabilities, not its correctness.

### 9. Local-first inference is the default; remote is a fallback.
The anchor is meant for inference. Tools that need AI route to local engines first (MLX, llama.cpp), browser-local for tasks that fit, remote BYOK as last resort. The user's data does not leave their fleet unless the user explicitly opted in for that specific task. This is not a privacy bonus; it is the architecture.

### 10. Every feature serves sovereignty, resilience, or both.
Features that serve neither — convenience features that don't compromise these but also don't strengthen them — are candidates for cutting. The stack is large; the principles are what keep it coherent. When in doubt, cut.

### 11. All authority flows downstream from a human-issued root.
No agent, no tool, no consumer holds capabilities that don't trace, through cryptographic attenuation, to a Grant a human signed. The fabric's authority graph is rooted in humans, end of. Agents derive their capabilities from humans; humans never derive theirs from agents. Without this principle, the fabric is "anyone can call the API"; with it, even compromised agents are bounded by what humans authorized. (See D-Agents for the operational consequences.)

---

## Non-negotiables

These constraints define the private mesh. They are the operational consequences of the principles above. Anything that violates them is not the private mesh.

1. **Designed for continuous, asynchronous, partial failure.** Power cuts, intermittent connectivity, multi-device partitions are normal operating conditions, not error conditions. All primitives are local-first; all network operations are queued; all state is eventually consistent.

2. **No NakliTechie-operated server in the critical path.** Cloudflare, NetBird coordination, the user's anchor — all are user-chosen infrastructure. NakliTechie ships code, not service.

3. **No account on NakliTechie's side, ever.** Identity is the user's own keys, full stop.

4. **No telemetry. No phone-home. No analytics.** The stack does not know who its users are. Gleam telemetry stays within the user's fleet.

5. **All crypto happens on the client.** Transports see ciphertext. Always.

6. **BYOK everywhere — credentials never persist in stack-owned storage.** Cloudflare credentials, NetBird auth, LLM API keys, third-party API keys. Held in the FIF (encrypted at rest), decrypted in memory per session, forgotten on close.

7. **Append-only event-sourced data model.** Mutations are events appended to a log; current state is a projection.

8. **Loss of all FIF copies + loss of all paths to reconstruct the key = loss of access.** No NakliTechie recovery service. The user is sovereign and bears the responsibility. The fabric provides the architecture for the user to choose their own resilience model (multi-device copies in v1.0; Shamir/social recovery as v1.x options).

9. **Tool data is namespaced and grant-gated.** Tools cannot read each other's data without explicit user-issued grants.

10. **The Transport Protocol is the spec.** No single implementation is canonical. Multiple implementations are first-class.

11. **Local-first inference is the default.** Remote BYOK is a fallback for tasks the anchor can't handle. The user's data does not leave their fleet unless the user explicitly opted into a remote provider for that specific task.

12. **The mesh layer is optional but not exotic.** Users who already have Tailscale or similar use it. Users without one can adopt the NakliTechie wrapper with one tap. The fabric does not assume the mesh exists, but its existence makes everything better.

13. **Complexity is hidden by default, visible on demand.** Power users see operation queues, freshness indicators, sync status, error states. Dragged-along family members see "open app, use app." Both audiences are first-class; defaults serve the latter while the former always has access to the truth.

14. **All capability traces to a human-issued root Grant.** Every operation a tool, agent, or any consumer performs derives — through cryptographic attenuation — from a Grant a human signed. The fabric's authority graph has humans at every root. There are no exceptions; there is no "system-issued" Grant; there is no service-account model that bypasses this.

15. **Grant enforcement is server-side at every transport.** Origin headers do not gate access. Client-side checks do not gate access. Only server-side macaroon verification gates access. This is enforced uniformly across the Go Hub, the Cloudflare Worker, the Local Network transport, and any future implementation. The conformance test suite includes adversarial cases.

16. **Idempotency keys travel with every operation that has side effects.** Bridge calls, yes. Agent operations of any kind, yes. The keys are a reliability property for humans (Hook 4) and a security property under agent use — they bound the blast radius of a compromised or prompt-injected agent.

---

## Pairing UX

### v1.0 mechanisms (all three)

**A. QR code (primary).** Existing device displays QR containing one-time pairing capability + rendezvous endpoint + FIF integrity commitment. New device scans, prompts for passphrase, decrypts FIF, joins fabric. Camera-based; works phone-to-laptop, laptop-to-phone, phone-to-phone.

**B. Short numeric code (typed in).** Existing device displays 6-digit code. New device prompts user to enter it. Code unlocks the pairing capability on a shared rendezvous. Works when QR fails (bad lighting, broken camera, screen-to-screen on same device).

**C. Magic link (one-time URL).** Existing device generates a URL containing the pairing capability. User shares to new device via any channel (AirDrop, message, paste). New device follows URL, prompts for passphrase, joins. For "I'm at work, want to add my laptop, source device is at home." Magic link supports both internet-mediated and local-network-only modes (URL points at source device's local IP when applicable).

All three mechanisms must work offline-local where the topology allows (e.g., two devices on the same Wi-Fi can pair via QR or code without any internet).

### Deferred to v1.x (documented, not built)

**D. BLE proximity.** Devices discover each other via Bluetooth when physically close. User approves on source device, new device receives pairing capability over BLE. Best UX when devices are nearby. Limited by browser BLE API support (Chrome supports, Safari mostly doesn't). Considered for v1.x once the v1.0 mechanisms have surfaced what users actually need; if BLE clearly addresses a real friction, it's added.

**E. NFC tap.** Physical tap between two devices. Smallest UX surface — one gesture, no codes, no scanning. Limited to phones with NFC and browsers with NFC API support. Considered for v1.x.

Both D and E are real and worth supporting; neither is so critical that v1.0 must include them. The protocol design accommodates them without changes — they're additional ways to deliver the same pairing capability that QR/code/link deliver.

---

## Roadmap

### Phase 1: Foundation (months 1-3)
**Goals:** Fabric protocol spec locked. Identity, Grant, Vault, Sync, History primitives implemented with all six failure-model hooks. Three reference transports (Go Hub, Cloudflare Worker, Local Network) shipped and conformance-tested. Fabric SDKs in JavaScript and Go. Reference CLI consumer ships. FIF format finalized with layered envelope. Shared list tool built and used daily by Bhai + son + brother.

**Artifacts:**
- `fabric-spec-001-v1.0.md` — full protocol spec
- `fabric-sdk-js` — TypeScript/JavaScript library for browser consumers
- `fabric-sdk-go` — Go library for native binaries, daemons, the Hub itself, and the CLI
- `nakli-hub` — Go binary, self-hostable transport
- `nakli-cf-worker` — Cloudflare Worker reference implementation
- `nakli-local` — Local Network (mDNS) reference implementation (embedded in fabric-sdk-js)
- `nakli-cli` — reference CLI consumer (Go, on top of fabric-sdk-go) — power-user surface for queue/freshness/grants
- `nakli-conformance` — test suite for any Transport or SDK implementation
- The shared list tool (name TBD)
- Documentation for tool authors

### Phase 2: Local-first stack (months 4-6)
**Goals:** Anchor install script. Local inference daemon with MLX + llama.cpp routing. LLM primitive shipped with full local-first routing. NetBird-wrapper mesh layer bootstrapped. Gleam integration.

**Artifacts:**
- `nakli-anchor-installer` — curl|bash installer for Apple Silicon (x86 Linux soon after)
- `nakli-llm-server` — OpenAI-compatible local inference daemon with engine routing
- `nakli-mesh` — NetBird wrapper with NakliTechie setup UX
- `nakli-llm-sdk` — LLM primitive in the fabric SDK
- Gleam adaptations for private mesh observability
- Migration playbook for existing portfolio tools (optional adoption)

### Phase 3: Bridge and breadth (months 7-9)
**Goals:** Bridge primitive shipped. Reference adapters for common services. Second and third fabric-native consumer tools. Documentation polish. External tool authors can build.

**Artifacts:**
- Bridge primitive in the fabric SDK
- Reference adapters for: CourtListener, archive.org, banking APIs, Google Drive (for users who have one), GitHub, LLM provider APIs as Bridge endpoints
- Second consumer tool — likely a fabric-native Mehfil or Mahalla reimagining
- Third consumer tool — Bhai's choice from the running ideas list
- External tool author docs

### Phase 4: Maturity and ecosystem (months 10-12+)
**Goals:** Existing portfolio tools migrate at their own pace. External contributors arrive. Distributed FIF (Shamir, device-quorum) ships. Optional managed Relay tier launched. Public communication of the private mesh thesis.

**Artifacts:**
- v1.x FIF envelope types (Shamir, device-quorum, social-recovery)
- Native package installers (.pkg, .deb, .rpm) — Decision 10 deferred items
- Tart/Lima VM image alternative — Decision 10 deferred items
- Optional NakliTechie-hosted Hub tier
- Public Karpathy-style blog post / writeup
- Open call for external tool authors

### Phase 5+ (sketched, not committed)
- Cluster-distributed inference (multiple anchors pooled via Thunderbolt 5)
- Federated identity for households or small teams (multiple users on one anchor)
- BLE and NFC pairing
- Swift-native fabric SDK
- Public capability format proposal for cross-author tools

---

## Open decisions (status)

All resolved decisions are tracked in `private-mesh-decisions-v0.7.md`. Only one remains open by design:

1. ⏳ **O2 — Collaborator shaping.** What bounded modules son and brother each own. Open by design — resolves after `fabric-spec-001-v1.0.md` exists, since the spec defines natural module boundaries.

Previously-open decisions resolved in v0.5: O1 (Origin strategy → D-Origin, plus D-Consumers), O3 (Launch face → D-Brand), O4 (Monetization → D-Monetization).

---

## Risk register

- **Risk: protocol churn during development.** Three people building in parallel against an evolving spec will diverge. **Mitigation:** spec freeze gates per phase. Once a primitive's spec is locked, only conformance tests change; implementations stabilize.

- **Risk: maximalist scope eats focus.** Seven primitives, three transports, a mesh wrapper, an inference daemon, install scripts, observability integration. **Mitigation:** strict phase gates. Phase 1 must ship before Phase 2 starts. The shared list tool is the proof-of-life for Phase 1.

- **Risk: failure model is hard to get right.** Local-first, idempotent, eventually-consistent systems are famously easy to get wrong. **Mitigation:** the six hooks are explicit v1.0 requirements; conformance tests for each. The shared list tool stresses the failure model from day one (households go offline; family members edit concurrently).

- **Risk: the anchor doesn't materialize for users.** The thesis assumes the hardware becomes common. If it doesn't, the stack degrades to "fabric + Cloudflare Worker + Local Network" which is still useful but loses much of the strategic thrust. **Mitigation:** every layer must be useful independently. Don't build features that only work when all layers are present.

- **Risk: capability design tarpit.** **Mitigation:** macaroons are picked. The fabric uses them with discipline.

- **Risk: mesh-layer wrapper becomes a quagmire.** NetBird is mature; wrapping it in friendly UX is moderate work. Trying to build a Tailscale-equivalent from scratch is years. **Mitigation:** wrap, don't rebuild.

- **Risk: three-person team produces five-person divergence.** **Mitigation:** Bhai owns the spec; agents implement to spec; son and brother work on bounded modules with clear interfaces.

- **Risk: LLM landscape shifts under us.** Local inference engines evolve fast. **Mitigation:** the LLM primitive abstracts over the engine. `nakli-llm-server` is replaceable. The fabric talks to it via an OpenAI-compatible API.

- **Risk: power-user-and-family dual audience tension.** **Mitigation:** default hidden, visible on demand. Defaults must serve the family member; depths must be there for the power user.

---

## What success looks like

### End of Phase 1
- Fabric SDK real and tested
- Three reference Hub implementations conform to the spec
- Shared list tool in daily use by Bhai + at least one other family member
- An external tool author could read the docs and start building

### End of Phase 2
- Anchor install script real — Bhai's M4 Max Studio runs it as the reference deployment
- Local inference happens; LLM primitive routes correctly
- Mesh-layer wrapper live; phone-to-anchor works over cellular
- Gleam reports on the live private mesh

### End of Phase 3
- Bridge live; multiple external services accessible via fabric grants
- Three fabric-native tools shipped
- Documentation good enough that a stranger can build a tool

### End of Phase 4
- Distributed FIF ships; real users have set up Shamir or device-quorum recovery
- Private Mesh thesis publicly articulated
- At least one external tool author has shipped
- Clear monetization path (optional managed tier) live without compromising sovereignty
- Portfolio is no longer "tools that share aesthetics" — it's "a sovereign personal computing platform"

### What success does not look like
- Lots of users (not a growth play)
- Tailscale-replacement (the mesh wrapper is for NakliTechie users, not a market entrant)
- Cloud-services-replacement (the anchor doesn't try to be AWS)
- Enterprise adoption (zero interest in this direction)

---

## Next documents

- `private-mesh-decisions-v0.7.md` — running log of all decisions and rationale (companion to this doc)
- `fabric-spec-001-v1.0.md` — concrete protocol spec (foundation; everything else points at this)
- `fabric-sdk-js-spec-001-v1.0.md` — JavaScript SDK spec
- `fabric-sdk-go-spec-001-v1.0.md` — Go SDK spec
- `hub-spec-001-v1.0.md` — Go Hub transport implementation spec
- `cf-worker-spec-001-v1.0.md` — Cloudflare Worker transport implementation spec
- `local-network-spec-001-v1.0.md` — Local Network (mDNS) transport implementation spec
- `cli-spec-001-v1.0.md` — Reference CLI consumer spec
- `mesh-vision-001-v1.0.md` — NetBird wrapper as its own project
- `anchor-vision-001-v1.0.md` — install script and what it deploys
- `llm-spec-001-v1.0.md` — LLM primitive and `nakli-llm-server`
- `shared-list-spec-001-v1.0.md` — Phase 1 consumer tool
- `agent-handoff-fabric-v1.0.md` — for the coding agent
