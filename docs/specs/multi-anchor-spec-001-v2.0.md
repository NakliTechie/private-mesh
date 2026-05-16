# Multi-Anchor Cluster and Federation Specification

**Document:** `multi-anchor-spec-001-v2.0.md`
**Status:** v2.0 draft (Phase 2)
**Companion to:** `fabric-spec-001-v1.0.md`, `hub-spec-001-v1.0.md`, `mesh-netbird-spec-001-v2.0.md`
**Audience:** Implementers and operators of multi-anchor setups; reviewers thinking about federation.

This spec covers two related-but-distinct topics, deliberately in one document because they share a lot of mental model:

1. **Anchor cluster** — N Hubs (typically on one user's hardware fleet) acting as one logical fabric transport
2. **Federation** — N independent fabrics (different users, different roots-of-trust) cooperating through cross-fabric Grant delegation

Both are Phase 2. v1.0 ships single-anchor (one Hub per user, no federation). The architecture supports both; this spec describes the protocol additions and operational shape.

---

## Scope

This document specifies:
- The anchor cluster model: how multiple Hubs replicate state and behave as one transport
- The federation model: how distinct fabrics issue Grants to each other's principals
- Protocol additions (small set, mostly additive)
- Identity considerations (clusters share identity; federations don't)
- Failure modes
- Operational concerns

Out of scope:
- Generic distributed database concerns (we don't reinvent Raft / Paxos; we pick simpler models suitable for small clusters)
- Inter-organization governance (federation is technical; trust decisions are out-of-band)
- Single-anchor scaling (a single beefy anchor can serve a household; clusters are for resilience and topology, not throughput)

---

## Part 1: Anchor Cluster

### Concept

One user (one principal-id) runs multiple Hubs. Reasons:
- Two physical locations (home + office anchor)
- Resilience (laptop anchor + always-on Pi)
- Specialization (one anchor with GPUs for LLM, one anchor with storage for archive)
- Geographic distribution (one home, one with family abroad)

All anchors share the same user identity (same FIF root). They sync state amongst themselves. Consumers see "the fabric" as one logical surface, not "Hub A vs Hub B."

### Cluster model

The simplest model that works: **multi-primary with eventual consistency**.

- Every anchor is a primary; accepts writes
- All anchors replicate to all others (small N, so all-pairs is fine for N ≤ 5)
- Conflict resolution: per the fabric's existing semantics (vector clocks, append-only logs, application-level merge)
- For History: same hash-chain rules apply; if two anchors append concurrently to the same History stream, the conflict is detected (existing `conflict` error) and the application handles it

There is NO leader. There is NO consensus protocol. The fabric's append-only event model and vector clocks make this safe.

### Consumer experience

A consumer's FIF lists multiple transports of type `hub`, all pointing at different anchors. The consumer's SDK:
- Picks the best one per request (lowest preference number, lowest latency)
- Fails over if the preferred one is unavailable
- Idempotency keys make duplicate-write avoidance automatic

For freshness: each anchor reports its own freshness relative to its peers. A consumer connected to anchor A sees `freshness.peers_synced` listing anchor B and anchor C. If B is offline (per A's view), it shows up in `peers_missing`.

### Cluster protocol additions

#### `POST /fabric/v1/cluster/join`
A new anchor joins an existing cluster.

- Auth: a Grant with `cluster:join` scope (typically the operator's root Grant)
- Request: `{ existing_anchor_url, existing_anchor_public_key }`
- Process:
  1. New anchor authenticates to existing anchor with operator's Grant
  2. Existing anchor returns the cluster's current peer list and state snapshot
  3. New anchor adopts the snapshot, begins replication
  4. Existing anchor announces the new peer to other peers
- Response: `{ cluster_id, peers, snapshot_ref }`

#### `POST /fabric/v1/cluster/leave`
An anchor gracefully leaves.

- Auth: `cluster:manage`
- Effect: peers stop syncing to this anchor; the leaving anchor's data stays intact for archival but is no longer authoritative

#### `GET /fabric/v1/cluster/state`
Returns cluster topology.

- Auth: `cluster:read`
- Response:
  ```json
  {
    "ok": true,
    "data": {
      "cluster_id": "<ulid>",
      "anchors": [
        { "hub_id": "...", "url": "...", "role": "primary", "last_seen": "..." }
      ],
      "operator_principal": "..."
    }
  }
  ```

### Replication mechanics

Each anchor maintains:
- For each peer anchor: a cursor (last event ID synced from that peer per stream)
- An outbox: events appended locally that need to push to peers
- An inbox: events received from peers that need processing

Cycle (every `sync.poll_interval_seconds`, default 30s; or triggered by webhook):
1. For each peer:
   - Push events from outbox where peer's cursor < local head
   - Pull events from peer where local cursor < peer's head
2. Apply received events:
   - Verify peer's signature
   - Deduplicate by event_id
   - Insert into local events table
   - Update local stream heads
3. Update cursors atomically

This is plain peer-to-peer state machine replication. The fabric's append-only semantics + vector clocks make it correct without consensus.

### Conflict handling within a cluster

For Vault streams: concurrent writes from different anchors are detected via vector clocks. Both events persist; conflict events emit; application resolves.

For History streams: concurrent appends to the same stream are caught by the hash-chain check. The "winning" event is the one that reached a quorum first (in practice, the one whose write propagated to more peers). The "losing" event is rejected as `conflict`; the application retries with the new head.

The cluster does NOT use Raft, Paxos, CRDT-style ordering, or any global ordering protocol. The fabric is append-only and event-sourced; that's enough.

### When a cluster split-brains

Imagine cluster of 3 anchors. Network partition: anchor A on one side, anchors B+C on the other.
- A continues accepting writes from local consumers
- B and C continue accepting writes from their local consumers
- When the partition heals:
  - A's events propagate to B and C
  - B+C's events propagate to A
  - History streams: concurrent appends produce conflicts; applications resolve
  - Vault streams: both sides' events persist; vector clocks make concurrency detectable; applications resolve

This is the same model as single-anchor with offline consumers. The cluster does not pretend to be transactionally consistent.

### Identity

All anchors in a cluster share the same operator principal (same FIF root key). Each anchor has its own Hub-identity keypair (for signing freshness, peer auth, discharge macaroons). When provisioning a new anchor:
- Pair it via `nakli-cli identity pair` → adds a new device subkey to the FIF
- Run `nakli-hub init` on it → generates the Hub-identity keypair specific to this anchor
- Run `nakli-mesh init` → joins the mesh
- Run `nakli-cli cluster join --target <existing-anchor-url>` → joins the fabric cluster

### Mesh integration

Anchor cluster works best on top of the mesh (per `mesh-netbird-spec-001-v2.0.md`). The mesh provides:
- Private connectivity between anchors regardless of network
- Stable mesh IPs so anchor URLs don't change when networks change
- ACL enforcement at the mesh layer (anchors only talk to anchors)

Without the mesh: anchor cluster works over the open internet (each anchor exposes its Hub via HTTPS), but the operator is responsible for connectivity. The mesh just makes it nicer.

### Operational

```
# Bootstrap a cluster on a fresh anchor
nakli-cli cluster bootstrap

# Join an existing cluster from a new anchor
nakli-cli cluster join --target https://primary-anchor.mesh:7842

# List anchors in the cluster
nakli-cli cluster anchors

# Decommission an anchor
nakli-cli cluster decommission <hub-id>
```

### Performance considerations

- Replication overhead is O(N) per write for N anchors (every anchor pushes to every other)
- For N ≤ 5 (typical personal/family scale): negligible
- For N > 5: revisit. May need a hub-and-spoke topology or a gossip protocol. Not a v2.0 problem.

---

## Part 2: Federation

### Concept

Two **independent fabrics** (different root principals, different FIFs, different Hubs, different operators) want to cooperate. Examples:
- Bhai's fabric collaborates with his brother's fabric on a shared project
- A vendor's agent (running on the vendor's fabric) needs to read a specific stream on the customer's fabric
- A research collaboration where each researcher has their own fabric, but a shared data stream

Federation is NOT clustering. Federation is two distinct trust roots issuing Grants to each other's principals.

### Federation primitives

Federation is built on existing fabric mechanics:
- Each fabric has its own root principal and FIF
- Fabric A issues a Grant authorizing a principal in Fabric B to access a specific stream/namespace
- The Grant is a macaroon signed by A's root, with caveats limiting scope, identifying the recipient
- Fabric B's principal presents this Grant when accessing Fabric A's transport
- Fabric A's transport verifies normally — the signature chain leads to A's root, so it's valid

This already works in the v1.0 protocol. Federation is more about UX and conventions than protocol changes.

### What federation adds

The federation spec defines:
- A discovery mechanism for cross-fabric Grants (you have to know how to address a foreign principal)
- A pairing flow for "introducing" two fabrics
- Caveats specific to federation use
- Conventions for Bridge calls that span fabrics
- An out-of-band trust-establishment protocol (humans verify "this is really Bhai's brother's fabric")

### Cross-fabric principal addressing

A principal's identifier is `<ulid>`. Across fabrics, IDs may collide (low probability with ULIDs, but conceptually possible). The federation uses fully-qualified principal identifiers:

```
<principal-id>@<fabric-id>
```

Where `<fabric-id>` is a stable identifier for the fabric (typically the operator's root public key fingerprint, or a chosen human-readable handle published in the fabric's discovery endpoint).

Example: `01HMXA...@bhai.fabric` and `01HMYK...@brother.fabric`.

The `GET /fabric/v1/discover` endpoint returns a fabric-id alongside transport-id. Federated consumers learn fabric-ids during pairing.

### Federation pairing flow

Two fabrics establish a federation relationship by exchanging:
1. **Fabric IDs** (each other's root public keys / fabric handles)
2. **Trust acknowledgment** (a signed statement: "I, Bhai's fabric, recognize brother's fabric as legitimate")

Out-of-band:
- A and B's operators meet (in person, or trusted video call, or verify keys via multiple channels)
- They exchange fabric IDs
- Each writes a "federation-recognized" event to a special History stream:
  ```
  fabric.federation:recognized
  {
    "recognized_fabric_id": "brother.fabric",
    "recognized_root_pubkey": "...",
    "recognized_at": "...",
    "recognized_by_human": "<principal-id of operator>"
  }
  ```

After this, either fabric can issue Grants naming principals from the other fabric.

### Cross-fabric Grant minting

Bhai's fabric mints a Grant for his brother's principal:
```bash
nakli-cli grant mint \
    --recipient "01HMYK...@brother.fabric" \
    --primitive vault \
    --namespace shared-project \
    --operations read \
    --expires-in 90d \
    --output shared-project-read.macaroon
```

The macaroon's identifier records:
- `issued_by_principal: 01HMXA...@bhai.fabric`
- `recipient: 01HMYK...@brother.fabric`
- Scope and caveats as usual

Bhai delivers the macaroon to his brother (via any out-of-band channel — Signal, email, whatever). The brother's tools, when accessing Bhai's fabric's Hub, present this macaroon.

### Verification at Bhai's Hub

When brother's tool calls Bhai's Hub:
- Macaroon is signed by Bhai's root → signature verifies normally
- The recipient field is checked against the requesting principal's signature on the request
- The brother's principal presents a self-signed request proving they hold the private key for `01HMYK...@brother.fabric`
- Bhai's Hub verifies brother's signature against the brother's pubkey (which the Hub knows because the federation pairing event recorded it)
- Operation proceeds

The protocol addition needed: requests across federation include a self-signature field that proves the requester is who the macaroon names. In v1.0 this is implicit (the macaroon is bound to a transport host); in federation it's explicit.

### Federation-specific caveats

- `from-fabric == <fabric-id>` — restricts Grant to be usable only from a specific foreign fabric
- `to-fabric == <fabric-id>` — for outgoing Bridge calls, restricts destination fabric
- `inherit-budget == false` — by default, federation Grants count against the issuer's budgets, not the recipient's; this caveat opts out

### Revocation across federation

If Bhai wants to revoke brother's access:
- Standard revocation: write to revocation stream
- Brother's tools see `grant_revoked` next time they try
- No special federation handling needed; the existing mechanism works

### Failure modes

- **Foreign fabric unreachable:** brother's tool can't reach Bhai's Hub → queues operations → retries
- **Foreign fabric retires the federation relationship:** "federation-derecognized" event in History; existing Grants from that fabric become unverifiable
- **Foreign fabric is compromised:** brother revokes his root → all his outgoing Grants become unverifiable; Bhai's fabric notices via the discharge mechanism (if Bhai's Grants to brother carry `discharge-from <brother's fabric>`)
- **Operator dispute:** out of scope; technical resolution can't fix social disputes

### What federation is not

- **Not a public network of fabrics.** No directory, no marketplace. Federation relationships are private, mutually established.
- **Not anonymous.** Each fabric knows the other's fabric-id and operator principal.
- **Not transitive by default.** If A federates with B, and B federates with C, A and C are NOT automatically federated. They federate explicitly if they want.
- **Not a way to monetize.** Federation does not entail billing. If service exchange has commercial terms, those are out-of-band.

### Operational

```
nakli-cli federation list
nakli-cli federation introduce --fabric-id brother.fabric --pubkey <hex>
nakli-cli federation derecognize <fabric-id>
nakli-cli grant mint --recipient <principal>@<fabric-id> ...
```

The UX is intentionally low-key. Federation is for users who already understand the model. v2.x may add gentler onboarding if real demand shows up.

---

## Cluster + Federation together

A federated fabric can itself be a cluster:
- Bhai runs an anchor cluster (3 anchors)
- Brother runs a single anchor
- Both fabrics are federated
- Brother's principals access Bhai's fabric via whichever of Bhai's 3 anchors is reachable
- Bhai's mesh + cluster transparency handles the routing

This works without special handling. The federation is between fabrics (identified by root); the cluster is internal to one fabric.

---

## What this means for the Phase 1 protocol

The Phase 1 protocol already supports both. Specifically:
- Macaroons can be issued cross-principal (no fabric assumed); the recipient field carries the principal-id
- Vector clocks and idempotency keys make multi-writer replication safe
- The freshness model makes "this anchor has only seen events up to X" explicit

What's NOT in Phase 1 but IS needed for cluster + federation:
- `POST /fabric/v1/cluster/join`, `leave`, `state` endpoints — additive; can be added in v1.x without breaking v1.0 consumers
- Fully-qualified principal addressing (`@fabric-id`) — convention; v1.0 IDs are unique enough but federated deployments will want this
- Federation pairing as a top-level concept in the CLI — UX layer
- Discharge for cross-fabric revocation — already supported via the existing discharge-from caveat

The protocol is forward-compatible. v1.0 ships single-anchor; v2.0 adds the small set of additions above; existing tools and SDKs work unchanged in both modes.

---

## Why one document for both

These are conceptually adjacent — both are about "how does the fabric scale to more than one node" — and the same primitives serve both. Splitting them into separate docs would risk redundancy and inconsistency. The cluster section is more developed (Phase 2 ships clusters); the federation section sketches enough for the architecture to be sound but doesn't over-specify before real usage informs it.

---

## Out of scope

- Cross-cluster cluster (cluster of clusters) — unnecessary complexity
- Federation directory / discovery service — would compromise the private-relationships model
- Auto-healing of split-brain (the existing conflict model is sufficient)
- Multi-tenancy within one fabric (one fabric = one operator; family members are principals not tenants)
- Quotas across federation
- Pricing / settlement / billing across federation
- Cross-fabric LLM route sharing — Phase 3 thought; the LLM primitive's peer-routed route gestures at it

---

## References

- Fabric protocol: `fabric-spec-001-v1.0.md`
- Hub spec: `hub-spec-001-v1.0.md`
- Mesh spec: `mesh-netbird-spec-001-v2.0.md`
- Macaroon paper (Birgisson et al., NDSS 2014) — the attenuation model is what makes federation safe
- Vision doc: `private-mesh-vision-001-v0.7.md` (multi-anchor as Phase 2)
