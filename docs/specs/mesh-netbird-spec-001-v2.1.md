# Mesh Layer (NetBird Wrapper) Specification

**Document:** `mesh-netbird-spec-001-v2.1.md`
**Status:** v2.1 draft (Phase 2)
**Supersedes:** `mesh-netbird-spec-001-v2.0.md` — substantially simplified: NetBird now ships an embed library (`github.com/netbirdio/netbird/client/embed`, May 2026) that lets `nakli-mesh` link the entire NetBird client into a Go binary rather than wrapping a separate `netbird` agent process. Phase 2 implementation drops from ~5-6 sessions to ~2.
**Companion to:** `fabric-spec-001-v1.0.md`, `hub-spec-001-v1.1.md`, `local-network-spec-001-v1.1.md`
**Audience:** Implementers of the NakliTechie mesh-layer wrapper; operators deploying multi-device meshes.

The mesh layer sits BELOW the fabric. Per D9, we wrap NetBird (not Headscale, not from-scratch WireGuard) as the underlay for cross-network peer connectivity. The fabric is transport-agnostic; the mesh layer makes one particular transport mode (peer-to-peer Hub-to-Hub, peer-to-peer LLM routing, etc.) work over the open internet without exposing services to it.

This is Phase 2. The fabric ships and works in Phase 1 without a mesh (transports use public URLs, local network, or Cloudflare Worker). The mesh adds:
- Private peer-to-peer connectivity across networks
- Survivable identity for moving devices (laptop on home Wi-Fi → café Wi-Fi → cellular)
- A unified address space for "my fleet of machines and people I trust"

---

## Scope

This document specifies:
- What we wrap from NetBird (and what we don't)
- The wrapper CLI (`nakli-mesh`)
- Integration with fabric identity (one FIF, one mesh node identity)
- Integration with fabric peer concept (Hub-to-Hub, anchor cluster discovery)
- Configuration shape (mostly thin over NetBird's config)
- Operational concerns: deploy, upgrade, debug
- Security posture (the boundary between NakliTechie and NetBird)

Out of scope:
- NetBird's internal protocols (WireGuard, STUN/TURN, signaling) — those are NetBird's documentation
- Replacing NetBird or running without it
- Cross-mesh federation (one user's mesh talking to another user's mesh as peers) — deferred to v2.x
- Mobile mesh nodes (iOS / Android) — NetBird supports these; the wrapper does too transitively, but mobile UX is its own thing

---

## Dependencies

### Required

- **`github.com/netbirdio/netbird/client/embed`** — the NetBird embed package, released May 2026. Embeds the entire NetBird client (WireGuard interface, signaling, NAT traversal, peer discovery) into our Go binary. Replaces the v2.0 spec's plan to wrap a separate `netbird` daemon process.
- **`fabric-sdk-go`** — for FIF reading, principal/Grant orchestration.

### NOT a dependency

- **NetBird CLI / agent binary.** v2.0 of this spec assumed we'd wrap the `netbird` agent; v2.1 supersedes that. The embed library makes a separate daemon unnecessary.
- **NetBird Management Service.** Per D9 we still need a management service for signaling; the operator runs the NetBird self-hosted stack OR uses NetBird's hosted offering. The embed library client connects to whichever management URL the operator configures.

### Architectural consequence

`nakli-mesh` becomes a single Go binary that:
1. Reads the user's FIF for identity material.
2. Initializes an `embed.Client` with a NetBird management URL and a setup key (derived from FIF principal).
3. Calls `Client.Start()` to bring up the WireGuard interface in-process.
4. Exposes the mesh peer addresses to the local Hub (via a local socket or HTTP endpoint).

No separate process management. No CLI scraping. No `netbird` agent. Just a Go program that links the NetBird client.

---

## Why wrap, not reimplement, not just-use

Per D9 rationale (in `private-mesh-decisions-v0.7.md`):
- Headscale: too tied to Tailscale's surface; mesh-of-meshes story unclear
- WireGuard from scratch: solid foundation but we'd reinvent NetBird's good parts (key rotation, automatic NAT traversal, multi-hop routing, group policies)
- NetBird as-is: too much surface; users would learn NetBird, not NakliTechie

The wrapper:
- Hides NetBird's account/team concepts under the fabric's principal/Grant model
- Auto-configures NetBird from the user's FIF (one identity to manage)
- Pre-templates common mesh topologies (single-user multi-device, family, small team)
- Surfaces only the controls that matter

**What we wrap:** identity reconciliation, configuration, lifecycle, mesh topology, exposure of mesh peer addresses to the Hub. **What we use from the embed library:** WireGuard data plane, signaling, NAT traversal, peer discovery — all in-process via `embed.Client`.

---

## Architecture (v2.1 — embed library based)

```
┌──────────────────────────────────────────────────────────┐
│ Fabric (transports, primitives, tools)                   │
│   Hub  ↔  Hub  ↔  Cloudflare Worker  ↔  Local Network    │
└──────────────────────────────────────────────────────────┘
                          ↑
                    fabric peer URLs (private)
                          ↓
┌──────────────────────────────────────────────────────────┐
│ nakli-mesh (single Go binary)                            │
│   ├── reads FIF for identity                             │
│   ├── derives NetBird config from principal ID           │
│   ├── embeds netbird.Client in-process                   │
│   ├── runs WireGuard interface                           │
│   └── exposes mesh peer addresses to local Hub           │
│                                                           │
│   Uses: github.com/netbirdio/netbird/client/embed        │
└──────────────────────────────────────────────────────────┘
                          ↓
              ┌───────────────────────┐
              │ NetBird Management    │
              │ Service               │
              │ (self-hosted or       │
              │  netbird.io)          │
              └───────────────────────┘
                          ↓
                    Open Internet
```

The embed library does the heavy lifting. `nakli-mesh` is a configuration-and-orchestration shim. There is no separate `netbird` daemon process to manage.

---

## Identity reconciliation

NetBird has its own identity model (users, devices, teams). The wrapper maps fabric identity to NetBird identity:

| Fabric concept | NetBird concept |
|---|---|
| Principal (human) | NetBird user |
| Principal (agent) | NetBird user (with `[agent]` tag) |
| Device (FIF subkey) | NetBird device |
| Mesh of peers | NetBird network (or "tenant") |
| Grant for `mesh:peer` operation | NetBird group membership + ACL |

The wrapper handles this mapping. The user thinks in fabric terms; NetBird does what it does underneath.

### Self-hosted NetBird vs hosted

NetBird offers a hosted control plane (netbird.io) and a self-hosted option. The wrapper supports both:
- **Hosted (default for first-time setup):** uses NetBird's hosted dashboard; user creates a free account; wrapper authenticates and configures peers
- **Self-hosted (sovereign path):** user runs `netbird-management` and `netbird-signal` on a server (often the same anchor as the Hub); wrapper authenticates against the local management API

Configuration determines which:
```toml
[mesh]
backend = "hosted"  # or "self-hosted"
management_url = "https://api.netbird.io"  # or self-hosted URL
```

For the sovereign-maximalist path: self-hosted. For "I just want it to work": hosted.

---

## `nakli-mesh` CLI

The wrapper exposes a small CLI on top of the NakliTechie SDK.

### Commands

```
nakli-mesh init                          # Set up NetBird, generate keys, join/create mesh
nakli-mesh status                        # Show mesh state
nakli-mesh peers                         # List peers in the mesh
nakli-mesh add-peer --pair-token TOKEN   # Add a peer via fabric pairing
nakli-mesh remove-peer PEER-ID           # Remove a peer (signs revocation event)
nakli-mesh route TARGET                  # Show how this device reaches TARGET (debugging)
nakli-mesh upgrade                       # Upgrade NetBird underneath
nakli-mesh diagnose                      # Run network diagnostic
nakli-mesh down                          # Disconnect from mesh
nakli-mesh up                            # Reconnect
```

The CLI is intentionally small. Power users who want full NetBird surface can run `netbird` directly; the wrapper does not hide it.

### `nakli-mesh init` flow

```
1. Read FIF (using fabric-sdk-go).
2. Check if NetBird is installed; if not, prompt user to install (or auto-install on platforms supporting it).
3. Generate NetBird setup key from fabric principal:
   - Hosted mode: prompt for NetBird account auth (one-time browser flow)
   - Self-hosted mode: use the management API token (configured separately)
4. Register this device with NetBird as principal_id-device_id (visible in dashboard).
5. Add to default group "nakli-mesh-<principal-id-short>".
6. Apply default ACL: allow within group, deny external.
7. Wait for WireGuard tunnel up.
8. Test connectivity to one known peer (if any).
9. Write mesh state to ~/.config/nakli-mesh/state.json.
```

### Integration with fabric pairing

When the operator pairs a new device via `nakli-cli identity pair`:
1. Standard fabric pairing completes (FIF subkey enrolled)
2. The new device's `nakli-mesh init` joins the same NetBird group
3. Once joined, the new device's Hub (or fabric tools) can reach existing peers over the mesh

The pairing flow is fabric-level; the mesh joining is a side effect handled by the wrapper.

---

## Configuration

`/etc/nakli-mesh/config.toml` (Linux) or `~/Library/Application Support/nakli-mesh/config.toml` (macOS):

```toml
[mesh]
backend = "hosted"                    # or "self-hosted"
management_url = "https://api.netbird.io"
signal_url = "https://signal.netbird.io"

[mesh.identity]
fif_path = "~/.config/nakli-cli/identity.fif"
# Setup keys, NetBird tokens stored encrypted under FIF root key

[mesh.network]
# Default: NetBird auto-assigns subnet
auto_subnet = true
# Override if multiple meshes overlap:
# preferred_subnet = "100.81.0.0/24"

[mesh.acl]
# Default policy: allow within group, deny external
default = "allow-within-group"
# Custom policies:
# [[mesh.acl.rule]]
# name = "anchor-fleet-only"
# from = "group:anchors"
# to = "group:anchors"
# protocol = "tcp"
# ports = [7842]   # fabric protocol port

[mesh.hub_integration]
# Auto-register Hub instances as fabric peers when discovered on the mesh
auto_register_hubs = true
# How often to scan for new Hubs on the mesh
scan_interval_seconds = 60
```

---

## Hub integration

The wrapper makes Hub-to-Hub fabric sync work transparently across the mesh.

### Discovery

The wrapper periodically (per `scan_interval_seconds`) probes each peer in the mesh for a Hub:
```
GET http://<peer-mesh-ip>:7842/fabric/v1/discover
```

If a Hub responds: the wrapper registers it with the local Hub via the standard fabric peer mechanism:
```
POST /fabric/v1/sync/add-peer
  body: { peer_id, mesh_url: "http://<peer-mesh-ip>:7842", public_key }
```

The local Hub now syncs with the remote Hub over the WireGuard tunnel.

### Failover

If the WireGuard tunnel drops:
- NetBird re-establishes (usually within seconds; relay used if direct fails)
- Hub sync resumes
- The Hub's freshness indicator reflects the gap

If NetBird is down entirely:
- Mesh peers become unreachable via mesh
- Cloudflare Worker transport (if configured) still works
- Local Network transport still works on same-LAN peers
- Queue catches up when mesh returns

The wrapper's `nakli-mesh status` surfaces all this.

---

## Mesh topologies

The wrapper templates a few common topologies. Choose at `init` time.

### Solo
- One person, multiple devices (laptop, desktop, phone, anchor)
- All in one group; full mesh among them
- Most common case

### Family / household
- Multiple humans (operator + participants); each may have multiple devices
- One mesh; per-person sub-groups
- ACL: anchors share with everyone; tool stream is per-namespace at fabric layer (not mesh layer)
- The wrapper does NOT control fabric stream access; that's Grants. The mesh just provides connectivity.

### Small team
- Multiple humans, multiple anchors, contractor agents
- One mesh; groups per role (anchors, humans, agents)
- ACL: anchors talk to anchors and humans; agents talk only to anchors
- Coordinates with fabric Grants but does not replace them

### Anchor cluster (advanced; ties to multi-anchor spec)
- One human, multiple anchors (M4 Pro, M4 Max Studio, NAS, etc.)
- Anchors mesh fully; humans connect to nearest anchor
- See `multi-anchor-spec-001-v2.0.md` for the fabric-level treatment

The wrapper picks reasonable defaults for each; advanced users edit ACLs directly via the NetBird dashboard or `netbird` CLI.

---

## Security posture

### What the mesh provides
- Encrypted point-to-point connectivity (WireGuard)
- Identity-bound device authentication (NetBird's key exchange)
- ACL enforcement at the mesh layer (defense-in-depth)
- NAT traversal without exposing services to the internet

### What the mesh does NOT provide
- Fabric-level authorization (that's Grants/macaroons)
- End-to-end encryption of fabric payloads (already done client-side per fabric protocol)
- Defense against malicious peers within the mesh (Grants gate access at the fabric layer)
- Authentication of users (the mesh authenticates devices; fabric authenticates principals via FIF)

### Trust boundary

The wrapper trusts NetBird. NetBird sees:
- Mesh metadata (which devices, when online, traffic volumes)
- Device public keys
- Optionally: traffic flow patterns (if metrics enabled)

NetBird does NOT see:
- Fabric payloads (already encrypted before reaching the mesh)
- FIF contents
- Grant contents (macaroons travel encrypted)

For users worried about NetBird's hosted control plane seeing metadata: self-host NetBird. Then NetBird is a process on the user's anchor with no external dependency.

### Key rotation

NetBird handles its own key rotation per its config. The wrapper does NOT manage WireGuard key material directly. Fabric FIF root keys are independent of mesh keys; rotating one does not require rotating the other.

---

## Operational

### Install

```bash
nakli-mesh install   # downloads and installs NetBird binary
nakli-mesh init       # joins the mesh
```

The install command is platform-specific:
- macOS: downloads NetBird .pkg, runs it
- Linux: adds repo + apt-get install, or downloads .deb/.rpm
- Other: prints instructions

### Upgrade

```bash
nakli-mesh upgrade
```

Upgrades NetBird underneath. The wrapper pins to a tested NetBird version range; arbitrary upstream versions are not supported.

### Logs

NetBird logs go to its standard locations (`/var/log/netbird/` on Linux, `~/Library/Logs/NetBird/` on macOS). The wrapper does not duplicate logs; it tails NetBird's logs when needed via `nakli-mesh diagnose`.

### Diagnose

```
nakli-mesh diagnose

Mesh status:
  Backend:        hosted (netbird.io)
  Connection:     UP (52s uptime)
  Local mesh IP:  100.81.0.42

Tunnels:
  PEER                 ROUTE        RTT     STATUS
  m4-pro (Bhai)        direct       3ms     up
  m4-max (Bhai)        direct       2ms     up
  nas-rack (Bhai)      direct       5ms     up
  ipad (Bhai)          relay        24ms    up
  worker-vm (cloud)    direct       9ms     up

Issues:
  - ipad relayed via TURN; check upstream NAT (symmetric NAT detected)

Recent events:
  10:34:21  Peer ipad came online
  10:30:00  Tunnel renegotiated
```

---

## Comparison to alternatives

| Property | NakliTechie wrapper + NetBird | Tailscale | Headscale | Just SSH | Just Hub on the open internet |
|---|---|---|---|---|---|
| Sovereignty | High (self-host option) | Low (Tailscale controls coordination) | High | High | High (but exposed) |
| NAT traversal | Yes | Yes | Yes | No | No (port forward) |
| Mesh topology | Yes | Yes | Yes | No | No |
| Identity-coupled to fabric | Yes (via wrapper) | No (separate) | No | No | N/A |
| Setup difficulty | Low (init wizard) | Lowest | High | Low | Medium (port forward + TLS) |

The wrapper's job is to make the NetBird path feel as easy as Tailscale's while preserving the sovereign-host option.

---

## What gets versioned

- Wrapper version: independent semver (e.g., `nakli-mesh 1.0.0`)
- NetBird version: pinned range (e.g., `netbird ^0.30.0`)
- The wrapper's `upgrade` command updates both within their compatible ranges

---

## Out of scope

- Mesh federation across users (one mesh talking to another mesh as peers) — v2.x or later; fabric peers can already cross meshes via shared NetBird groups
- Mesh-of-meshes (recursive grouping) — v3+ if ever
- Replacing NetBird with a different backend — D9 is locked; revisit if NetBird becomes unmaintained or moves in a wrong direction
- Mesh-level encryption of fabric payloads (already done at fabric layer; double-encryption gains nothing)
- Mesh-layer DNS (NetBird's MagicDNS suffices)
- Mobile-only UX polish — defer until mobile fabric consumers are real

---

## References

- NetBird: https://netbird.io
- Decision D9 (NetBird wrap)
- Fabric protocol: `fabric-spec-001-v1.0.md`
- Hub spec: `hub-spec-001-v1.0.md`
- Multi-anchor: `multi-anchor-spec-001-v2.0.md` (sibling Phase 2 spec)
