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

## Forward-compatibility

Phase 1 reserves namespaces, endpoint paths, and behaviors that Phase 2 / v1.x will fill in. See "Forward-compatibility hooks" in [the agent handoff](docs/specs/agent-handoff-fabric-v1.2.md).

## Further reading

- [Vision](docs/vision-v0.7.md) — design philosophy and rationale
- [Decisions](docs/decisions-v0.7.md) — locked design decisions
- [Wire protocol](docs/specs/fabric-spec-001-v1.0.md) — every endpoint, header, and error code
