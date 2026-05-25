# nakli-local Specification

**Document:** `local-network-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `local-network-spec-001-v1.0.md` — adds explicit dependency choices (mDNS library, WebRTC library) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md`, `fabric-sdk-js-spec-001-v1.0.md`, `hub-spec-001-v1.0.md`
**Audience:** Implementers of the Local Network transport; consumers using it.

`nakli-local` is the Local Network transport: two or more Fabric consumers on the same physical network sync directly with each other, without going through any external transport. mDNS for peer discovery; WebRTC data channels (or local HTTPS) for the protocol.

This is the most failure-resilient mode (everything else can be down, this still works) and the most sovereign (no third party at any layer). It is promoted to v1.0 status (per D5 revision) because it serves real scenarios the other transports can't: ISP outage, offline office, household on home Wi-Fi during WAN downtime.

**Critical:** This transport is embedded in `fabric-sdk-js` (browser) and `fabric-sdk-go` (native binaries). It is not a standalone binary. Each consumer participating in the Local Network mesh runs the embedded transport.

---

## Scope

This document specifies:
- Discovery mechanism (mDNS / DNS-SD)
- Connection establishment (WebRTC for browser, direct HTTPS for native)
- Protocol implementation over the chosen connection
- Authentication and trust (using the Fabric Grant model)
- Peer-to-peer sync semantics
- Browser API constraints and workarounds
- Conformance with `fabric-spec-001-v1.0.md`

Out of scope:
- Cross-subnet discovery (mDNS is link-local; multi-subnet is future work)
- Mobile cellular/hotspot mode (treated as ordinary local network if the host supports peer discovery)
- Internet-mediated NAT traversal (this is the Hub or mesh-layer's job)

---

## Dependencies

### Required (Go side: `nakli-local-bridge` and Hub if it does mDNS)

- **`github.com/grandcat/zeroconf`** — canonical Go mDNS library. Pure Go (no dependencies), supports both registering services and browsing.
- **`github.com/pion/webrtc/v4`** — the canonical Go WebRTC implementation. NetBird itself depends on `pion/ice`; we use the higher-level `pion/webrtc` for our signaling fabric.
- **`fabric-sdk-go`** — protocol + macaroon verification.

### Required (Browser side)

- **Native WebRTC API** (`RTCPeerConnection`) — no library needed. All modern browsers support this since 2020+.
- **Native fetch API** — for HTTPS to the local bridge.
- **`fabric-sdk-js`** — for protocol + macaroon verification.

### Forbidden

- libp2p or other heavyweight P2P frameworks. Our use of WebRTC is narrow (browser-to-browser signaling for the local network); pion/webrtc + native WebRTC is enough.
- Custom mDNS implementation. zeroconf is the standard.

---

## Discovery: mDNS / DNS-SD

### Service definition

The Local Network transport advertises and discovers peers via mDNS using the service type:

```
_nakli-fabric._tcp.local.
```

Each consumer announces:
- Instance name: `<principal_id_short>-<device_id_short>` (e.g., `01HMX...-01HMZ...`)
- Port: dynamically assigned (50000-50100 typical)
- TXT records:
  - `version=naklimesh/1.0`
  - `principal_id=<full ULID>` (or hash for privacy)
  - `device_id=<full ULID>`
  - `transport_id=<ULID for this transport instance>`
  - `capabilities=vault,history,sync,grant,identity` (subset of primitives this consumer offers)
  - `webrtc_signal_endpoint=<path on local HTTP server, if used>`

Consumers also browse for `_nakli-fabric._tcp.local.` and maintain a list of discovered peers.

### Browser limitation

Browsers do NOT have a general mDNS API. Workarounds in browser context:
- Companion **mDNS bridge** runs as a small native helper OR is part of the Hub/CLI running on the same machine
- Bridge exposes a local HTTP/WebSocket endpoint (e.g., `http://127.0.0.1:7849/local-discover`) that browsers query
- Bridge does mDNS browsing and announces; reports discovered peers to browser via WebSocket
- For browsers without a bridge: Local Network transport is non-functional. Tools fall back to other transports.

Bridge process:
- Bundled with `nakli-cli` (runs alongside CLI when needed)
- Bundled with `nakli-hub` (the Hub announces itself and listens for local peers)
- Standalone `nakli-local-bridge` binary (lightweight: ~10 MB) for users running only browser tools

### Native consumer (Go SDK)

Native consumers use a Go mDNS library (`github.com/grandcat/zeroconf` or `github.com/hashicorp/mdns`) directly. No bridge needed.

### Peer announcement lifecycle

- On `Fabric.unlockFIF()` succeeding: announce via mDNS
- On `Fabric.lock()` or process exit: send mDNS goodbye
- Re-announce every 90 seconds (mDNS standard)
- TTL on records: 120 seconds

---

## Connection establishment

Two consumers on the same network establish a Fabric Protocol connection. Two paths depending on consumer type:

### Path A: Native ↔ Native (Go SDK ↔ Go SDK)

Direct HTTPS. Each Go consumer runs an HTTP server on a local port (announced in mDNS).

- Connection: `https://<peer-local-ip>:<port>/fabric/v1/...`
- TLS: self-signed certs (pinned via the device subkey public key in the FIF)
- Each request includes `X-Fabric-Grant` per protocol
- No NAT traversal needed; same network

The Go consumer's HTTP server is essentially a mini-Hub: it implements the protocol endpoints for the streams it owns.

### Path B: Browser ↔ Browser (JS SDK ↔ JS SDK)

WebRTC data channels. Browsers cannot run servers on local ports.

- The mDNS bridge facilitates WebRTC signaling between peers on the same network
- Browser A wants to talk to Browser B:
  1. Browser A asks bridge: "peers on the network?"
  2. Bridge responds with peer list including Browser B
  3. Browser A initiates WebRTC offer; sends to Browser B via bridge's signal relay
  4. Browser B accepts; sends answer via bridge
  5. Browsers establish direct WebRTC data channel
  6. Protocol messages flow over the data channel
- The data channel carries JSON messages framed as protocol request/response pairs
- Bridge sees signaling messages (SDP, ICE candidates) but NOT the protocol payloads (those go peer-to-peer)

### Path C: Browser ↔ Native (JS SDK ↔ Go SDK)

The Go consumer's HTTP server is reachable from the browser via `https://<peer-local-ip>:<port>/fabric/v1/...`. Mixed-content / certificate trust issues:
- Self-signed cert is hard for browsers; user accepts cert pinning prompt or operator installs a local CA
- Alternative: Go consumer also accepts WebRTC connections (acts as both HTTP server and WebRTC peer), bridge facilitates signaling

For v1.0, **the recommended pattern is**: the Hub (or a CLI running locally) is the Go consumer that owns the canonical stream state. Browser tools talk to it via either:
- HTTP if the user has set up cert trust (operator's choice)
- WebRTC via bridge if they haven't

This makes browser ↔ browser sync practical only when at least one peer also runs the bridge or a Hub.

---

## Protocol over the transport

### Native (HTTPS)

Identical to Hub protocol implementation. Same endpoints, same request/response shapes, same headers. The only difference is the URL.

### Browser (WebRTC data channel)

The data channel carries framed JSON messages:

```json
// Request frame
{
  "type": "request",
  "request_id": "<ulid>",
  "method": "POST",
  "endpoint": "/fabric/v1/vault/append",
  "headers": {
    "X-Fabric-Version": "naklimesh/1.0",
    "X-Fabric-Grant": "<base64>",
    "X-Fabric-Idempotency-Key": "<ulid>"
  },
  "body": { ... }
}

// Response frame
{
  "type": "response",
  "request_id": "<ulid>",
  "status": 200,
  "headers": { "X-Fabric-Version": "naklimesh/1.0" },
  "body": { "ok": true, "data": { ... }, "freshness": { ... } }
}
```

Message size limits:
- WebRTC data channel: typically 16 KB per message; consumers chunk larger payloads
- Chunked messages use a continuation flag:
  ```json
  { "type": "request", "request_id": "...", "chunk": 0, "more": true, "data_part": "..." }
  ```

### SSE / Subscribe over WebRTC

WebRTC data channels are bidirectional, so Subscribe is implemented as a long-lived series of response frames from a single request:
```json
{ "type": "response", "request_id": "<sub-id>", "status": 200, "stream": true, "body": null }
// then 0..N event frames:
{ "type": "stream-event", "request_id": "<sub-id>", "event": { ... } }
// finally:
{ "type": "stream-end", "request_id": "<sub-id>" }
```

---

## Authentication and trust

### Per Grant — same as other transports

Every request carries a macaroon Grant. The local transport verifies the Grant exactly as the Hub does. There is no special "local network trust" — Grants are required.

**Agent operations on the local network** (per D-Agents): an agent on a local peer device holds Grants exactly as it would on any other transport; verification is identical; the local transport enforces macaroon attenuation server-side just like Hub and Worker. Local Network does not create a privileged path for agents and does not relax Grant requirements for "trusted" same-network peers.

### Peer identity verification

When a consumer discovers a peer via mDNS:
- The TXT record includes `principal_id` and `device_id`
- The consumer looks these up in its FIF or local cache of known principals
- If the peer is recognized: trust as expected, establish connection
- If the peer is unrecognized: treat as untrusted; only allow operations explicitly authorized (e.g., a guest device with a one-time pairing Grant)

### Defense against spoofing

mDNS records can be spoofed. Defenses:
- The peer's `transport_id` is signed by the peer's device key
- On connection establishment, request a challenge/response signed by the device key
- Refuse connections if the signature doesn't match the public key in the consumer's known principals

The challenge endpoint:
```
GET /fabric/v1/identity/challenge?nonce=<base64>
→ Response: signature over nonce + transport_id + timestamp, using the device's keypair
```

Consumers MUST verify this challenge before sending Grants over the connection.

---

## Peer-to-peer sync semantics

Local Network peers sync events the same way as Hub-to-Hub sync, but in a multicast pattern:

- Each peer maintains a list of discovered local peers
- On Vault/History append: peer pushes the event to all locally-discovered peers
- Each receiving peer verifies the event signature, Grant authorization, and applies to local state
- On disconnect (mDNS goodbye or timeout): peer is removed from active list

### Conflict surfaces

When two peers on the local network write to the same stream concurrently:
- Both events propagate to all peers
- Each peer sees both events
- Vector clocks make the concurrency detectable
- Consumers emit `conflict` events to their tools

### Sync with remote transports

A consumer connected to BOTH local peers AND a remote Hub:
- Acts as a gateway: events from local peers are also pushed to the Hub
- Events from the Hub are also pushed to local peers
- Idempotency keys prevent duplicates
- This is opportunistic; sync is best-effort, not transactional

If no consumer is gateway-capable (e.g., everyone is browser-only and the Hub is unreachable), local peers sync amongst themselves; when the Hub returns, the next-connected consumer reconciles.

### LAN-anchor pattern (ephemeral Vault peer)

Pure peer-to-peer sync only works when at least two peers are simultaneously online. For LAN deployments where devices come and go at different times (e.g., a team chat app on the office network, where laptops open and close at different times), at least one peer must hold events while the others are offline.

The canonical solution is the **LAN anchor**: an always-on device on the same network running `nakli-hub --storage=ephemeral`. It is a full Vault peer (implements the entire fabric protocol, verifies macaroons, advertises via mDNS), but its event storage is a bounded RAM ring buffer (per `hub-spec-001-v1.1.md` §Storage modes). Restart loses events; the transport identity is preserved so peers' fingerprint pins still hold.

Sequence:
1. Operator installs `nakli-hub` on an always-on LAN device (NAS, Pi, always-on laptop).
2. Starts it with `--storage=ephemeral` (or sets `storage.mode = "ephemeral"` in config).
3. Hub announces via mDNS with `storage=ephemeral` in its TXT record and `storage_profile: "ephemeral"` in `/fabric/v1/discover`.
4. Other LAN peers discover it through the bridge (or directly, for native consumers).
5. Apps send Vault appends to this peer just like they would to a durable Hub or the CF Worker. The peer buffers events; later-arriving peers fetch what they missed.

**This pattern replaces the antipattern of "ship a custom buffered bridge alongside the app."** Consumer tools do not build their own store-and-forward layers. If LAN S&F is the requirement, an ephemeral Hub answers it; the bridge stays out of the protocol path.

**Durability obligation:** any namespace served by an ephemeral-only LAN-anchor is one restart away from data loss. Deployments SHOULD configure at least one durable peer (a remote Hub on the user's anchor box, or `nakli-cf-worker`) so the canonical event history survives.

**vs the bridge:** the bridge is a discovery + signaling helper. It does not hold Vault state. The LAN anchor is a separate process; on small deployments they may run on the same machine, but they are conceptually distinct.

---

## Browser-specific implementation

### mDNS bridge interface

The browser SDK looks for a bridge at well-known local addresses:
- `http://127.0.0.1:7849`
- `http://[::1]:7849`

Bridge endpoints (out of band; not the Fabric Protocol):

#### `GET /local/peers`
Returns currently discovered peers:
```json
{
  "ok": true,
  "data": {
    "peers": [
      {
        "transport_id": "<ulid>",
        "principal_id": "<ulid>",
        "device_id": "<ulid>",
        "ip": "192.168.1.42",
        "port": 50001,
        "version": "naklimesh/1.0",
        "capabilities": ["vault", "history", "sync"]
      }
    ]
  }
}
```

#### `WebSocket /local/peers/observe`
Pushes peer-list updates as devices come and go.

#### `POST /local/signal`
Relays WebRTC signaling messages between browser peers.
- Request: `{ to_transport_id, signal_type: "offer"|"answer"|"ice-candidate", payload }`
- Response: `{ ok: true }`
- Bridge forwards `signal_type` and `payload` to the target peer (if locally reachable)

The bridge does NOT process Fabric Protocol; it only facilitates discovery and signaling. The browser's JS SDK uses the bridge for these out-of-band operations.

**This is a deliberate boundary.** Apps that need LAN store-and-forward MUST NOT extend the bridge to hold envelopes or implement Vault endpoints. The bridge stays thin so its trust posture stays simple (sees only mDNS announcements + WebRTC signaling envelopes; never holds protocol state, never verifies macaroons, never stores ciphertext). For LAN S&F, run an ephemeral Vault peer alongside the bridge — see §LAN-anchor pattern.

### Bridge availability detection

```typescript
const fabric = new Fabric();
const localTransport = await fabric.transports.tryLocalNetwork();
if (localTransport) {
  console.log("Local transport available via bridge");
} else {
  console.log("No local bridge; fallback to other transports");
}
```

### Self-signed cert handling (Native HTTP peer)

For Browser → Native HTTP peer, the browser must trust the peer's self-signed cert. Options:
- Operator installs a local CA into their browser's trust store (one-time, manual)
- Use the WebRTC path instead (bridge-mediated)

The SDK prefers WebRTC when both options are available, to avoid the cert-trust UX issue.

---

## Failure scenarios

### Bridge crashes
Browser SDK detects bridge unavailability (HTTP timeout); marks Local Network transport unavailable; falls back per transport selection logic. When bridge returns, transport is re-enabled automatically.

### Peer leaves network
mDNS announces goodbye, OR the peer is detected as unreachable on next sync cycle. Removed from active list; pending events to that peer queue for next reconnect.

### Network partition (subnet split)
Peers on different sides of the partition see different sets of peers. They sync with whoever's reachable. On reunion, the next reconnect cycle reconciles. Vector clocks and idempotency make this safe.

### Adversary on the same network
Defenses (described above):
- mDNS challenge/response with device-key signature
- Grant verification at protocol level (no trust just because someone's local)
- Encrypted payloads (transport sees ciphertext)

A malicious peer on the network can:
- See your mDNS announcements (your principal_id is visible)
- Initiate a WebRTC connection to you (you can refuse if their device_id is unknown)
- See ciphertext if they intercept WebRTC traffic (encryption is end-to-end at the payload level)

A malicious peer cannot:
- Forge a Grant (cryptographic signature)
- Read your data (encrypted)
- Impersonate your devices (no private key access)

### Mobile hotspot mode
When a user shares their phone's hotspot with a laptop, both devices are on the same network. Discovery works; sync works. Treat as ordinary local network.

---

## Performance characteristics

- Discovery latency: 1-3 seconds (mDNS announcement propagation)
- Connection establishment: < 500 ms (WebRTC) or < 100 ms (HTTPS)
- Event throughput: limited by local network bandwidth (~100 MB/s LAN, ~50 MB/s Wi-Fi)
- Concurrent peers: 2-10 typical; 20+ in office scenarios

---

## Comparison vs other transports

| Property | Hub (durable) | Hub (ephemeral, LAN-anchor) | CF Worker | Local Network (browser ↔ browser) |
|---|---|---|---|---|
| Requires internet | No (LAN to Hub OK) | No | Yes | No |
| Requires user-run server | Yes | Yes (small, on LAN) | No | Bridge (small, on LAN) |
| Storage profile | `durable` | `ephemeral` | `durable` | n/a (peers only) |
| Sovereignty | Highest | Highest | Cloud (Cloudflare) | Highest |
| Failure resilience | Single point | Restart loses events | Cloud uptime | Per-peer |
| Setup complexity | Moderate | Trivial | Easy | Trivial (auto-discover) |
| Scale | Self-tunable | 1-25 peers | Cloud-scale | 1-25 peers |
| Push delivery | SSE | SSE | SSE / polling | WebRTC stream |
| Cross-subnet | Yes | No (LAN-bound) | Yes | No |

Operators typically run a combination: a durable Hub on their anchor box (or `nakli-cf-worker` in the cloud) as the canonical store, plus an ephemeral Hub on the LAN as the anchor that browser-first peers reach via the bridge. The bridge itself never holds state — it just helps peers find each other and signals WebRTC connections.

---

## Conformance

The Local Network transport MUST pass the conformance suite. Run via:

```bash
nakli-cli conformance --target local://<peer-transport-id>
```

The CLI handles peer discovery and routes requests through the local transport.

Known limitations vs Hub (these MUST be documented but are NOT conformance failures):
- No cross-subnet discovery
- Browser ↔ browser requires bridge availability
- Subscribe via WebRTC has different framing than SSE (consumers handle both)

---

## Out of scope for v1.0

- DNS-SD over wider-than-LAN (multicast DNS is link-local by design)
- WiFi Direct or AirDrop-style direct peer connection without a network
- Pre-shared key authentication (Grants are the only auth)
- Bluetooth Low Energy peer discovery (deferred; companion to BLE pairing in v1.x)

---

## References

- mDNS: RFC 6762
- DNS-SD: RFC 6763
- WebRTC data channels: https://www.w3.org/TR/webrtc/
- Go mDNS: https://github.com/grandcat/zeroconf
- Protocol spec: `fabric-spec-001-v1.0.md`
- JS SDK spec: `fabric-sdk-js-spec-001-v1.0.md`
- Go SDK spec: `fabric-sdk-go-spec-001-v1.0.md`
- Decisions: D5 (transport plurality, Local Network v1.0 promotion)
