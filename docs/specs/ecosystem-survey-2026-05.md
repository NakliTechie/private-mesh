# Ecosystem Survey and Competitive Benchmarking

**Document:** `ecosystem-survey-2026-05.md`
**Status:** Survey, May 2026
**Purpose:** Position the NakliTechie Private Mesh against the active ecosystem. Identify overlap, differentiation, naming risks, and forward-pressure decisions that may need to be revisited.
**Audience:** Bhai. Read after vision/decisions/specs are stable, before commitments to direction.

We never did this. Work has begun on Phase 1 nonetheless. This survey is a sanity check: is the shape still right, given what others are actually shipping? Are we accidentally rebuilding something, or accidentally collision-bound on a name, or accidentally building toward a sunset?

The short answer: the shape is right and largely uncontested at the level we're building. There are three real adjacencies worth knowing about — one is uncomfortably close (Keyhive), two are parallel rather than competitive. There is one naming collision (Microsoft Fabric) that is search-dominant and worth a conscious decision about.

---

## The ecosystem in five buckets

### 1. Local-first / CRDT / sync engine ecosystem
**Examples:** Automerge, Yjs, ElectricSQL, Triplit (acquired by Supabase 2025), TanStack DB, LiveStore, Replicache, RxDB, p2panda.

**What they do:** Provide the data substrate for offline-first, multi-device applications. CRDT or sync-engine-based merge, IndexedDB/SQLite-WASM/OPFS persistence, mostly opinionated about the data model.

**Where we overlap:** Our Vault and History primitives are append-only event stores with vector clocks. Our `fabric-merge-helpers` is in spirit a small CRDT library. Saanjha uses fractional indexing — straight out of this ecosystem.

**Where we differ:**
- They are libraries you compose with your own architecture. We are a full stack: identity, authorization, transports, primitives, and tools.
- They are typically opinionated about one merge model (Yjs: rich-text-shaped; Automerge: JSON CRDT; ElectricSQL: SQL/Postgres). We let tools choose. Append-only event sourcing + tool-owned merge.
- They don't ship an authorization layer. We do.
- They don't ship an agent-as-principal model. We do.

**Assessment: complementary, not competitive.** If we wanted to use Automerge underneath instead of our own append-only model, we could. We chose our model for two reasons: (a) the platform is event-sourced anyway because of History/audit; running CRDT semantics on top would be redundant; (b) tool-owned merge with helpers is simpler than imposing CRDT semantics on every tool. Both choices remain defensible.

### 2. Capability-based authorization ecosystems
**Examples:** UCAN (user-controlled authorization network), Macaroons, biscuit-auth, zcap-ld, SPKI/SDSI.

**What they do:** Bearer tokens with delegated, attenuable, time-bounded authority. Verification offline. Chain of provenance from a human-issued root.

**Where we overlap:** Heavy. Our Grant model is macaroons. The UCAN spec explicitly compares itself to Macaroons. Both describe the same conceptual space: caveat-bearing, attenuating, signed capabilities.

**Where we differ:**
- We chose macaroons. UCAN chose JWT-on-steroids with DIDs. Different wire format, similar semantics.
- UCAN's "actor" model is well-developed for the agent era; they were thinking about delegated AI agents earlier than most.
- We bake authorization into the protocol; UCAN is a token standard you bring to any system.

**Assessment: parallel approaches, no winner yet.** Macaroons are battle-tested, simpler, and faster (HMAC vs Ed25519 verification). UCAN has stronger DID/PKI integration and a more active spec community. Both are valid. Our choice (D1) is defensible and the protocol is forward-compatible enough that a v2 fork could swap if needed.

**One real risk:** if MCP or A2A converges on JWT/OAuth-2.1 + Token Exchange (RFC 8693) as the agent authorization standard (current trajectory per the searches), our macaroon-based system is at the edge of the mainstream. We can interoperate (the Bridge primitive plus an MCP shim — already noted as v1.x in `bridge-adapters-spec-001-v1.0.md`), but we're not riding the standard wave.

### 3. Keyhive (Ink & Switch) — closest competitor

**What it is:** A capabilities + group-management + E2EE layer designed for local-first Automerge documents. Brooklyn Zelenka and Alex Good have been working on it since 2024. Active as of May 2026, FOSDEM 2026 talk just happened.

**What it solves:**
- "Convergent capabilities" (concap): a new capability model between object-cap and certificate-cap, partition-tolerant, CRDT-compatible
- Group management CRDT with coordination-free revocation
- E2EE with causal keys and post-compromise security
- "Beelay" sync protocol that lets a server hold only encrypted blobs and still help with sync

**Where it overlaps with us:**
- *Substantially.* Read the lab notes and the design rhymes with ours: macaroon-like delegated capabilities, post-compromise security via key rotation, encrypted-at-rest with the storage layer holding only ciphertext, the "users own their data" framing.
- Keyhive deliberately excludes user identity (FIF equivalent) and leaves it for higher layers. Our FIF + envelope types fits that gap.
- Their "Beelay" sync protocol is conceptually adjacent to our Hub/Worker transports.

**Where we differ:**
- **Scope.** Keyhive is a library; we are a stack. They give you primitives; we give you a usable mesh + transports + CLI + tools.
- **Audience.** Keyhive is for developers building local-first Automerge apps. We are for the operator-plus-participant model (households, small groups), with agents as principals.
- **Identity.** They punt; we have a complete FIF model.
- **Agent-era.** Their work doesn't yet center agents-as-principals the way our D-Agents does.
- **Stage.** Keyhive is research-grade and just landed in their GAIOS prototype. We are at "phase 1 in flight" with a clearer ship target.

**Assessment: this is the project to watch.** Not as a competitor — neither of us is trying to capture the other's audience — but as the most credible neighbor. Two specific actions worth considering:

1. **Track their releases.** If Keyhive ships as a usable library, our `fabric-sdk-go` and `fabric-sdk-js` could use it under the hood for the auth + E2EE pieces. Macaroons are simpler today; concap may turn out to be the right call for partition-tolerant systems long term.
2. **Acknowledge them explicitly.** A line in the vision doc that says "we are adjacent to Automerge/Keyhive; we chose macaroons over concap because [X]" preempts the "why don't you use Automerge" question every reviewer will ask.

### 4. MCP / A2A / agent-coordination protocols
**Examples:** Anthropic's Model Context Protocol (MCP), Google's A2A (Agent-to-Agent), IBM's ACP (now merged into A2A), the Agentic Identity work at CoSAI, Cerbos/Gravitee/Strata MCP authorization runtimes.

**What they do:** Define how AI agents discover and call tools (MCP), coordinate with each other (A2A), and authenticate/authorize themselves (OAuth 2.1 + RFC 8693 Token Exchange). Industry-driven, fast-moving, enterprise-focused.

**Where we overlap:**
- Both ecosystems care about agent identity, delegation, attenuation, audit.
- Both care about "the agent acts on behalf of the human."
- Our Bridge primitive is conceptually MCP-shaped (adapter + operation + params).

**Where we differ:**
- They are converging on OAuth 2.1 + DCR + RFC 8693 (token exchange) + W3C DIDs + verifiable credentials. We are macaroons + ed25519 + FIF.
- They are enterprise-shaped. We are personal/family/small-group-shaped.
- They route agent traffic through identity gateways (Strata, Arcade, Cerbos PDPs). We route through transports + Grant verification.
- They assume a corporate IDP (Auth0, Okta, WorkOS) issues identities. We assume the user issues their own.

**Assessment: parallel, will need to interoperate eventually.** The right v1.x move is exactly what we already planned: an MCP shim that exposes Bridge adapters as MCP tools, so MCP-aware agents can use our fabric without us adopting MCP semantics fabric-wide.

**One opportunity:** the MCP ecosystem is loud about agents-with-fine-grained-permissions but the systems that exist (Cerbos, Gravitee, Strata) are all enterprise SaaS or open-source-with-enterprise-pricing. There is genuinely no good personal-scale answer. Our spec lands in that gap — even if we never market it that way.

### 5. Personal data servers / sovereign social
**Examples:** Bluesky PDS (43M users on AT Protocol), Solid Pods, Nextcloud, ATmosphere apps, Periwinkle (managed PDS hosting), Mastodon.

**What they do:** Self-hosted (or managed-self-hosted) data servers for social or productivity. Federation, identity portability, "you own your data."

**Where we overlap:**
- The "you own your data" framing.
- Self-hosted server pattern (Hub ≈ PDS).
- Federation as Phase 2 concept.

**Where we differ:**
- They are social-network-shaped (or document-shaped). We are capability-fabric-shaped.
- They federate at the protocol level (everyone speaks ATproto/ActivityPub). We federate at the macaroon level (one fabric can issue Grants for another).
- They are public-facing by default; we are private-by-default.
- Their PDS is one piece of a larger network (the Relay, the AppView, etc.). Our Hub is the whole platform.

**Assessment: different problem space.** Useful to study for operational patterns (curl|bash installers, federation UX, account migration). Not a competitor.

### 6. Apple Silicon clusters / personal AI hardware
**Examples:** Exo Labs (1.0 just shipped, RDMA over Thunderbolt 5), MLX, Mac mini clusters (Marco Arment's Overcast cluster being the canonical example), Vitalik Buterin's self-sovereign LLM setup.

**What they do:** Make it possible to run frontier-grade AI models on your own hardware. Cluster Macs (or Linux+NVIDIA), pool memory, run models locally.

**Where we overlap:**
- Our anchor concept assumes this hardware exists.
- Our LLM primitive's `anchor-local` route depends on `nakli-llm-server` which wraps MLX/llama.cpp.
- Our Gleam project is explicitly a fleet observability tool for Apple Silicon home clusters with RDMA support.

**Where we differ:**
- Exo is "how to share compute across your devices." We are "how to coordinate, authorize, and audit work that uses those devices."
- They sit below us in the stack. We are their natural application layer.

**Assessment: foundational layer, not a competitor.** The Mac-cluster ecosystem makes our anchor-local LLM routing economically reasonable for personal use. We benefit from their work; they don't need ours.

---

## Naming collisions

### Microsoft Fabric — SERIOUS
- "Fabric" as a Microsoft product is now ~30,000 customers, the fastest-growing data platform in Microsoft's history (per FabCon 2026 coverage).
- Searches for "fabric protocol", "fabric SDK", "fabric capability" return Microsoft Fabric results dominantly.
- They use exactly the same phrases: "data fabric", "fabric layer", "fabric IQ".
- Our `fabric-spec`, `fabric-sdk-go`, `fabric-sdk-js`, `naklimesh/1.0` protocol string — all collide in search and in conversation.

**Decision worth making explicit:** do we keep "fabric" or do we rename? Options:
- **Keep "fabric" internally; brand externally as "Private Mesh"** — the existing decision. Mostly works because we never market ourselves as "Fabric"; the term is internal vocabulary. But every developer who reads our docs sees "fabric" everywhere.
- **Rename the protocol** to remove "fabric" — significant churn given Phase 1 has begun. e.g. "mesh-protocol", "naklimesh-protocol", or something more distinctive.
- **Accept the collision** — Microsoft Fabric is enterprise data; we are sovereign personal computing. Different audiences, low actual confusion in context.

My recommendation: accept the collision but acknowledge it in docs ("not to be confused with Microsoft Fabric — different product, different audience"). The cost of renaming Phase 1 in flight outweighs the search-pollution risk for our small-audience product.

### Bluesky AT Protocol — minor
- "AT Protocol" is the Bluesky federation protocol. Our protocol version string is `naklimesh/1.0` so we don't collide on the string itself.
- Some terminology overlap on "personal data server" but we don't use that phrase.

### NakliTechie tool names — clean
- "Saanjha" (working name for shared list): not in use elsewhere.
- "nakli-hub", "nakli-cli", "nakli-mesh": not in use.
- "Mehfil", "Mahalla", "Bahi", "Kagaz", etc.: domain is ours.

---

## Forward pressure: what might force a revisit

These aren't urgent. They're worth knowing about so we're not surprised in six months.

### 1. MCP authorization convergence
If MCP becomes the de facto agent-tool protocol AND it converges hard on OAuth 2.1 + RFC 8693 (current trajectory), our macaroon-native model becomes a deliberate choice we have to defend. The v1.x MCP shim plan handles this technically. The marketing question is harder: "why don't you just use MCP?"

**Mitigation:** Phase 2 should include the MCP shim. Treat it as a Phase 2 spec, not a v1.x afterthought.

### 2. Keyhive landing as a usable library
If Ink & Switch ships Keyhive as a real, drop-in library in late 2026, the question "why aren't you on top of Keyhive?" becomes a real one. Keyhive's convergent capabilities are arguably better than macaroons for our use case (CRDT-native, coordination-free revocation).

**Mitigation:** track their releases. The fabric-sdk-go and fabric-sdk-js are abstracted enough that the auth + E2EE pieces could be swapped to Keyhive under the hood without breaking consumers, if it ever made sense.

### 3. Browser CRDT consolidation
If Yjs + Automerge effectively become "the" CRDT layer for browser-based local-first software (they're already close), tools built on our `fabric-merge-helpers` might feel like they're missing out on the richer ecosystem. Our Vault model is not CRDT in the strict sense; it's append-only with tool-owned merge.

**Mitigation:** none needed for v1.0. If a tool wants Automerge inside, the Vault can store Automerge documents as opaque payloads — the platform doesn't care.

### 4. Apple Silicon cluster as a platform
If Exo becomes the standard way to run home AI on Mac fleets, our `nakli-llm-server` plans should integrate with it rather than compete. The LLM routing spec (`llm-routing-spec-001-v2.0.md`) doesn't preclude this — Exo could be an `anchor-local` backend — but we haven't explicitly planned for it.

**Mitigation:** when writing `nakli-llm-server` spec for Phase 2, treat Exo as a first-class backend option alongside MLX-Python and llama.cpp.

### 5. Sovereignty narrative going mainstream
Cloud repatriation, sovereignty as a board-level concern, the "self-sovereign LLM" pattern (Vitalik's piece is one of many) are all in motion. This is *good* for us — the audience is being primed. But it also means enterprise vendors will pivot to claim the same space.

**Mitigation:** keep the audience clarity sharp. We are personal/family/small-group. We are not enterprise. Resist the urge to move upmarket.

---

## What this doesn't change

- The vision (v0.7) stands.
- The decisions (v0.7) stand.
- The 15 specs stand.
- The Phase 1 milestones in `agent-handoff-fabric-v1.1.md` stand.
- The shape — sovereignty, browser-native, agent-aware, BYOK, no telemetry — is uncontested at our altitude.

We didn't accidentally build a duplicate of anyone. The closest neighbor (Keyhive) is at a different layer and a different stage.

---

## What this does change (small additions, optional)

1. **Add an "Adjacent work" section to the vision doc** that names Keyhive, UCAN, Automerge, Bluesky PDS, MCP/A2A, and Exo. One paragraph each. Reviewers will ask. Better to answer upfront than in every conversation.

2. **Promote the MCP shim from v1.x to Phase 2.** Currently mentioned in `bridge-adapters-spec-001-v1.0.md` as "v1.x explores". The MCP ecosystem is moving fast; if we wait, we are behind. A small Phase 2 spec on "MCP shim for Bridge adapters" is cheap and forward-positions us.

3. **Add a NakliTechie/Microsoft Fabric disambiguation note** somewhere visible — README, vision doc preamble, or both. Acknowledging the collision is much cheaper than renaming.

4. **Reach out to Ink & Switch.** Not formally — just an email or note. They are open to research collaboration, they have working code, and our roadmaps may end up sharing engineers (Brooklyn Zelenka is also in the UCAN community). Worth a conversation; costs nothing.

5. **Consider an `ECOSYSTEM.md` in the repo root** that captures this survey in compressed form. The agent will see it during M0 skeleton work and the rationale stays visible.

---

## Bottom line

The Private Mesh sits in a genuinely uncrowded spot: personal-scale capability fabric with agents-as-principals, BYOK, single-HTML tools, and a sovereign-first operational shape. The adjacent work is real but not duplicative. The biggest open question is naming (Microsoft Fabric collision), and the answer is probably "accept and disambiguate" rather than rename mid-flight.

Phase 1 ships as planned. Some small additions to docs would strengthen the positioning without changing the architecture.
