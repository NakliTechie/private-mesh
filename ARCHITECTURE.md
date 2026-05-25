# Architecture

Layer map for the NakliTechie Private Mesh. Top is closest to the human; bottom is the wire and the disk.

```
┌─────────────────────────────────────────────────────────────────────┐
│ Consumer tools (single-HTML, zero-account)                          │
│   roster (shared list, sibling repo)  ·  crate  ·  future tools     │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                  Fabric SDKs  (JS · Go)                              
                                  │
┌─────────────────────────────────────────────────────────────────────┐
│ Seven primitives                                                    │
│   Identity · Grant · Vault · History · Sync · LLM · Bridge          │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                Wire protocol (fabric-spec-001-v1.0)                  
                                  │
┌─────────────────────────────────────────────────────────────────────┐
│ Three transports (any one is sufficient; all are interchangeable)   │
│   nakli-hub   ·   nakli-cf-worker   ·   local-network (mDNS)        │
└─────────────────────────────────────────────────────────────────────┘
                                  │
┌─────────────────────────────────────────────────────────────────────┐
│ Storage (transport-local, ciphertext only)                          │
│   SQLite (Hub) · R2 + KV (Worker) · in-memory + disk (Local)        │
└─────────────────────────────────────────────────────────────────────┘
```

## Trust boundaries

- **Client-side encryption.** All Vault and History payloads are encrypted with per-namespace keys before they touch any transport. Transports never see plaintext.
- **Server-side authorization.** Macaroon Grants are verified at every transport on every request. The client never decides whether an operation is permitted.
- **FIF stays local.** The Fabric Identity File (FIF) — the user's root key material — never leaves the device that created it without an explicit user-driven backup operation. Tools never copy FIFs to "the cloud."
- **BYOK in memory only.** Bridge credentials are double-encrypted at rest inside the FIF and decrypted only in memory at call time.

## Storage and store-and-forward

**Vault is the store-and-forward primitive.** Every transport implements Vault; the durability profile is a per-deployment storage choice, not a protocol distinction. Two profiles are canonical:

- **`durable`** — crash-survivable disk storage with checksums. Events persist across restarts. The default for `nakli-hub` (SQLite + filesystem blobs) and `nakli-cf-worker` (R2 + KV).
- **`ephemeral`** — bounded-RAM ring buffer with age-based eviction. Events are lost on restart; only the transport's own identity (keypair) survives. Useful for LAN-anchor deployments where an always-on local peer buffers messages so intermittently-online devices on the same network can converge, while durability lives in another transport.

Every Vault peer advertises its profile via `GET /fabric/v1/discover` and (on the local network) via mDNS TXT. Consumers MUST treat ephemeral peers as sync convenience, not durable storage, and SHOULD configure at least one durable peer in the fabric to avoid total data loss.

This means consumer tools never build app-specific store-and-forward layers. If a tool needs LAN-resilient sync without a cloud anchor, the answer is "run a Vault peer with the ephemeral profile on a LAN device" — not "ship a custom buffered bridge alongside the app." See `docs/specs/hub-spec-001-v1.1.md` (Storage modes) for the ephemeral implementation; `docs/specs/local-network-spec-001-v1.1.md` for the LAN-anchor deployment recipe.

## Forward-compatibility

Phase 1 reserves namespaces, endpoint paths, and behaviors that Phase 2 / v1.x will fill in. See "Forward-compatibility hooks" in [the agent handoff](docs/specs/agent-handoff-fabric-v1.2.md).

## Further reading

- [Vision](docs/vision-v0.7.md) — design philosophy and rationale
- [Decisions](docs/decisions-v0.7.md) — locked design decisions
- [Wire protocol](docs/specs/fabric-spec-001-v1.0.md) — every endpoint, header, and error code
