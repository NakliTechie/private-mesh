# Fabric SDK (JavaScript) Specification

**Document:** `fabric-sdk-js-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `fabric-sdk-js-spec-001-v1.0.md` — adds explicit dependency choices (macaroon library, crypto libraries, ULID library) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md` (the protocol)
**Audience:** Implementers of `fabric-sdk-js`; consumers of `fabric-sdk-js`.

The JS SDK is the reference convenience library for browser-based Fabric consumers. It wraps the wire protocol with idiomatic JavaScript, handles transport selection, manages the operation queue, and exposes the failure-model hooks as developer-friendly APIs.

**Critical:** the SDK is a *convenience*. The protocol is the contract. Anything the SDK does, a consumer can do by speaking the protocol directly. This is by design (D-Consumers).

---

## Scope

This document specifies:
- Public API surface of `fabric-sdk-js`
- FIF management on the client
- Grant management and verification helpers
- Transport selection, fallback, and queuing
- Failure-model hook surface (queue inspection, freshness, conflicts)
- Subresource Integrity and CSP requirements for embedding in zero-build browser tools
- Conformance against the protocol spec
- The `fabric-merge-helpers` companion library

This document does NOT specify:
- Internal implementation details (storage, crypto library choices) — those are implementation concerns
- UI components for pairing, Grant management, queue inspection — those are tool concerns
- Bundling, transpilation, or build setup beyond what's needed for zero-build browsers

---

## Dependencies

The SDK MUST use the following libraries. Naming them explicitly prevents reimplementation of well-trodden infrastructure.

### Required

- **`macaroon`** (npm: `macaroon`, github: `go-macaroon/js-macaroon`) — macaroon serialization, signature chains, first-party and third-party caveats. Wire-compatible with `gopkg.in/macaroon.v2`. Supports v1 and v2 macaroons in JSON and binary formats.
- **`@noble/ciphers`** — XChaCha20-Poly1305 (`xchacha20poly1305` export). WebCrypto does not support XChaCha; this library provides it. Audited, zero dependencies, ~10 KB.
- **`hash-wasm`** — Argon2id (WASM). Fast and well-maintained. Used for FIF passphrase KDF.
- **WebCrypto (browser native)** — for HKDF-SHA256, Ed25519 sign/verify, SHA-256 hashing. Use via `crypto.subtle`. Modern browsers (since 2024) support all of these natively.
- **`ulidx`** (npm: `ulidx`) — ULID generation for event IDs, idempotency keys, principal/agent/device IDs. ~2 KB.

### Recommended

- **`idb`** (npm: `idb`) — thin Promise wrapper over IndexedDB. The operation queue and state cache use IndexedDB; idb removes the callback boilerplate.

### Forbidden

- Custom macaroon implementations. The `macaroon` npm package is wire-compatible with the Go side; rolling our own would create incompatibility risk.
- Custom Argon2id, XChaCha20-Poly1305, HKDF, or Ed25519. The named libraries are audited; reimplementation is not.
- Heavy frameworks (React, Vue, Svelte, etc.) inside the SDK. The SDK is framework-agnostic — consumers may use any framework or none.

### Zero-build embedding

The SDK MUST be distributable as a single ESM file that browser tools can import via `<script type="module">`. All dependencies above must therefore be available as ESM with no build step required. The build pipeline for the SDK bundles them into the single `fabric-sdk.js` artifact (and a `fabric-sdk.min.js`).

This is non-negotiable for the NakliTechie shape: tools must be able to consume the SDK via `import { Fabric } from "https://cdn.jsdelivr.net/npm/@naklitechie/fabric-sdk@1.0.0/fabric-sdk.js"` with no further toolchain.

---

## Distribution

`fabric-sdk-js` is distributed as:
- A single ESM file: `fabric-sdk.js` (~80-120 KB minified, gzipped to ~25-35 KB)
- A type declaration file: `fabric-sdk.d.ts`
- A subresource integrity hash (SHA-384) published with each release
- Available via CDN (cdn.jsdelivr.net), unpkg, or vendored alongside the tool

Zero-build embedding example:
```html
<script type="module">
import { Fabric } from "https://cdn.jsdelivr.net/npm/@naklitechie/fabric-sdk@1.0.0/fabric-sdk.js";
const fabric = new Fabric();
await fabric.unlockFIF("path/to/fif", passphrase);
</script>
```

The SDK MUST NOT require a bundler. It must work when dropped into a browser tool with no build step.

---

## Top-level API

### Class `Fabric`

The main entry point. Single instance per browser tab/principal.

```typescript
class Fabric {
  constructor(options?: FabricOptions);

  // FIF lifecycle
  unlockFIF(fifBytes: ArrayBuffer | File, passphrase: string): Promise<Identity>;
  createFIF(passphrase: string, principalName: string): Promise<{ fif: ArrayBuffer, identity: Identity }>;
  rotateEnvelope(newEnvelopeType: EnvelopeType, params: EnvelopeParams): Promise<ArrayBuffer>;
  lock(): void;

  // Identity
  identity: Identity | null;
  pair(method: "qr" | "code" | "link"): Promise<PairingArtifacts>;
  completePair(token: string, newDeviceName: string): Promise<DeviceEnrollment>;
  provisionAgent(spec: AgentSpec): Promise<AgentIdentity>;
  retireAgent(agentId: string, reason: string): Promise<void>;

  // Grants
  grants: GrantStore;

  // Primitives
  vault: VaultAPI;
  history: HistoryAPI;
  sync: SyncAPI;
  llm: LLMAPI;
  bridge: BridgeAPI;

  // Failure-model surface
  queue: OperationQueueAPI;
  freshness: FreshnessAPI;
  health: HealthAPI;
  events: EventBus;  // for conflicts, degradation, etc.

  // Transport management
  transports: TransportManager;
}
```

### `FabricOptions`

```typescript
interface FabricOptions {
  // Queue settings
  queueStorage?: "indexed-db" | "opfs" | "memory";  // default: "indexed-db"
  maxQueueSize?: number;  // default: 10000 operations
  retryBackoff?: BackoffConfig;  // default: exponential, 1s base, 60s cap

  // Transport selection
  transportTimeout?: number;  // ms, default: 5000
  fallbackOnUnavailable?: boolean;  // default: true

  // Failure-model hooks
  stalenessBudgetMs?: number;  // default: 86400000 (24h)
  emitDegradationEvents?: boolean;  // default: true

  // Cryptography
  cryptoProvider?: "subtle-crypto" | "noble";  // default: "subtle-crypto"

  // Logging
  logLevel?: "silent" | "error" | "warn" | "info" | "debug";  // default: "warn"
}
```

---

## FIF management

### `unlockFIF(fifBytes, passphrase)`

Decrypt and authenticate the FIF. Loads identity into memory.

```typescript
const identity = await fabric.unlockFIF(fileHandle, "correct horse battery staple");
// identity is now populated; vault/history/sync/llm/bridge APIs are usable
```

Throws:
- `FIFFormatError` — malformed FIF
- `FIFEnvelopeUnsupportedError` — envelope type not supported in v1.0
- `FIFAuthenticationError` — wrong passphrase or tampered FIF (MAC verification failed)

### `createFIF(passphrase, principalName)`

Generate a new root keypair and write a minimal FIF.

Returns:
- `fif` — ArrayBuffer to save to disk via File System Access API or download
- `identity` — populated identity object

### `rotateEnvelope(newEnvelopeType, params)`

Re-encrypt the FIF under a new envelope. v1.0 supports only `passphrase-only` → `passphrase-only` (passphrase change). v1.x will support cross-type rotation.

### `lock()`

Zero in-memory key material. Subsequent primitive calls throw `IdentityLockedError`.

---

## Identity surface

```typescript
interface Identity {
  principalId: string;
  principalType: "human" | "agent" | "device";
  publicKey: Uint8Array;
  rootKeypair: KeyPair;  // private key zeroed if locked
  devices: DeviceSubkey[];
  agents: AgentIdentity[];
  displayName: string;
  createdAt: Date;
}

interface AgentSpec {
  name: string;
  vendor: string;
  publicKey: Uint8Array;  // generated by the agent OR the SDK
  expiresAt?: Date;
}

interface PairingArtifacts {
  method: "qr" | "code" | "link";
  pairingToken: string;
  qrPayload?: string;  // base32-encoded for QR
  numericCode?: string;  // 6 digits
  magicLink?: string;
  expiresAt: Date;
}
```

### Pairing flows

```typescript
// Existing device
const artifacts = await fabric.pair("qr");
// artifacts.qrPayload → render to QR

// New device
const enrollment = await fabric.completePair(pairingToken, "MacBook Pro");
// enrollment.deviceId, enrollment.initialGrant, enrollment.transportConfigs
```

### Agent provisioning

```typescript
const agentKey = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign"]);
const agentIdentity = await fabric.provisionAgent({
  name: "claude-code-tax-prep",
  vendor: "anthropic",
  publicKey: await crypto.subtle.exportKey("raw", agentKey.publicKey),
  expiresAt: new Date(Date.now() + 30 * 24 * 60 * 60 * 1000),  // 30 days
});

// Mint a scoped Grant for the agent
const grant = await fabric.grants.mint({
  recipientPrincipalId: agentIdentity.agentId,
  scope: { primitive: "vault", namespace: "bahi", operations: ["read"] },
  caveats: [
    { type: "time-before", value: agentIdentity.expiresAt },
    { type: "rate", value: { count: 1000, window: "hour" } },
  ],
});
```

### Agent retirement

```typescript
await fabric.retireAgent(agentIdentity.agentId, "Tax season ended");
// Subsequent operations under agent's keys fail with grant_invalid
```

---

## Grant store and minting

### `GrantStore`

```typescript
interface GrantStore {
  mint(spec: GrantSpec): Promise<Grant>;
  verify(macaroon: string, hypothetical: HypotheticalOperation): Promise<VerificationResult>;
  revoke(grantId: string, reason: string): Promise<void>;
  list(filter?: GrantFilter): Promise<Grant[]>;
  get(grantId: string): Promise<Grant>;
}

interface GrantSpec {
  recipientPrincipalId: string;
  scope: {
    primitive: "vault" | "history" | "bridge" | "llm" | "sync" | "identity" | "grant";
    namespace: string | "*";
    operations: string[];
  };
  caveats: Caveat[];
  expiresAt?: Date;
}

type Caveat =
  | { type: "time-before"; value: Date }
  | { type: "time-after"; value: Date }
  | { type: "principal-type"; value: ("human" | "agent" | "device")[] }
  | { type: "agent-id"; value: string }
  | { type: "device-id"; value: string }
  | { type: "operation"; value: string[] }
  | { type: "namespace"; value: string }
  | { type: "rate"; value: { count: number; window: "second" | "minute" | "hour" | "day" } }
  | { type: "max-amount"; value: { amount: number; currency: string } }
  | { type: "only-domain"; value: string[] }
  | { type: "requires-human-approval" }
  | { type: "nondelegatable" }
  | { type: "idempotency-required" }
  | { type: "discharge-from"; value: string };  // verifier URL
```

### Grant verification helper

```typescript
const result = await fabric.grants.verify(macaroon, {
  primitive: "bridge",
  namespace: "*",
  operation: "call",
});
// result.wouldSucceed: boolean
// result.reasons: array of unmet checks
```

Useful for tools that surface "what can this agent do" before invoking operations.

---

## Vault API

```typescript
interface VaultAPI {
  append(spec: VaultAppendSpec): Promise<VaultAppendResult>;
  read(namespace: string, streamId: string, opts?: ReadOptions): Promise<VaultEvent[]>;
  listStreams(namespace: string): Promise<StreamSummary[]>;
  subscribe(namespace: string, streamId: string, opts?: SubscribeOptions): AsyncIterable<VaultEvent>;
}

interface VaultAppendSpec {
  namespace: string;
  streamId: string;
  event: {
    kind: string;
    payload: any;  // SDK encrypts before sending
    causalDependencies?: string[];
  };
  idempotencyKey?: string;  // auto-generated if not provided
}
```

### Encryption transparency

The SDK encrypts payloads transparently:
- Caller passes plaintext to `append`
- SDK derives the namespace key (HKDF over root key + namespace)
- SDK encrypts with XChaCha20-Poly1305
- Encrypted bytes are sent to the transport
- On read, SDK decrypts and returns plaintext

If decryption fails (key mismatch, tampered data), the SDK throws `VaultDecryptionError` for that specific event but continues with others.

### Vector clock and causal ordering

The SDK maintains the local device's vector clock and increments on each append. When appending, the SDK:
1. Increments local device's clock
2. Sets `vector_clock` on the event
3. Computes `causal_dependencies` based on local view of stream heads
4. Sends to transport

Consumers do not need to manage clocks manually for the common case.

### Subscribe (SSE)

```typescript
const stream = fabric.vault.subscribe("list", "shopping-list", { sinceEventId: lastSeen });
for await (const event of stream) {
  // handle event
}
```

The SDK manages reconnection on transport failure; the async iterable does not terminate on transient errors.

---

## History API

```typescript
interface HistoryAPI {
  append(spec: HistoryAppendSpec): Promise<HistoryAppendResult>;
  read(streamId: string, opts?: ReadOptions): Promise<HistoryEvent[]>;
  verify(streamId: string): Promise<{ verified: boolean; length: number; headHash: string }>;
}

interface HistoryAppendSpec {
  streamId: string;
  event: {
    kind: string;
    payload: any;
  };
  idempotencyKey?: string;
}
```

The SDK handles `previous_event_hash` automatically — it fetches the current head, sets the hash, attempts append, and on `conflict` either retries (refetching the head) or surfaces the conflict via `events`.

### Hash chain verification

```typescript
const result = await fabric.history.verify("audit-log");
if (!result.verified) {
  // chain broken; alert user
}
```

---

## Sync API

Sync is primarily managed by the SDK transparently. The API exposes:

```typescript
interface SyncAPI {
  status(): Promise<SyncStatus>;
  forcePull(peerId?: string): Promise<void>;
  forcePush(peerId?: string): Promise<void>;
  peers(): Promise<Peer[]>;
}

interface SyncStatus {
  peers: Array<{
    peerId: string;
    lastSyncAt: Date;
    freshnessMs: number;
    pendingEventsOut: number;
    pendingEventsIn: number;
  }>;
  overallFreshnessMs: number;
}
```

---

## LLM API

```typescript
interface LLMAPI {
  complete(spec: CompletionSpec): Promise<CompletionResult>;
  routes(): Promise<Route[]>;
}

interface CompletionSpec {
  messages: Message[];
  capabilities?: {
    minContextWindow?: number;
    needsVision?: boolean;
    needsFunctionCalling?: boolean;
  };
  preferredRoute?: "local" | "browser-local" | "remote" | "auto";
  maxCostCents?: number;
  idempotencyKey?: string;
}
```

### Browser-local inference

When `preferredRoute` is `"browser-local"` or `"auto"` and the route's available, the SDK uses Transformers.js or wllama (the SDK does not bundle these; tools provide them). The SDK provides an integration point:

```typescript
fabric.llm.registerBrowserBackend({
  name: "transformers-js",
  generate: async (messages, opts) => { ... },
  capabilities: { ... },
});
```

---

## Bridge API

```typescript
interface BridgeAPI {
  call(spec: BridgeCallSpec): Promise<BridgeCallResult>;
  adapters(): Promise<Adapter[]>;
  approve(pendingOperationId: string, approve: boolean, reason?: string): Promise<BridgeCallResult>;
  listPending(): Promise<PendingOperation[]>;
}

interface BridgeCallSpec {
  adapter: string;
  operation: string;
  params: Record<string, any>;
  dryRun?: boolean;
  idempotencyKey?: string;
}
```

### Human approval flow

When the Grant for a Bridge call carries `requires-human-approval`, the call returns:
```typescript
{
  status: "pending",
  pendingOperationId: "<ulid>",
  willExecuteIfApproved: { ... },
}
```

The UI presents the pending operation to the human; on approval, the tool calls `fabric.bridge.approve(...)` to execute.

---

## Operation queue API (Hook 1)

```typescript
interface OperationQueueAPI {
  size(): number;
  pending(): Promise<QueuedOperation[]>;
  retry(operationId: string): Promise<void>;
  cancel(operationId: string): Promise<void>;
  clear(filter?: QueueFilter): Promise<number>;
  observe(callback: (event: QueueEvent) => void): () => void;  // returns unsubscribe
}

interface QueuedOperation {
  id: string;
  primitive: string;
  endpoint: string;
  payload: any;
  attempts: number;
  lastAttemptAt: Date;
  nextAttemptAt: Date;
  lastError?: string;
  idempotencyKey: string;
}
```

The queue is persisted (IndexedDB by default) and survives page reload. Operations replay on next page load with the same idempotency keys.

### Queue events emitted

- `enqueued` — operation added
- `attempt` — operation tried
- `succeeded` — operation completed (200/202)
- `failed-permanent` — non-retryable error (removed from queue)
- `failed-retryable` — retryable error (will retry per backoff)

---

## Freshness API (Hook 3)

```typescript
interface FreshnessAPI {
  current(): FreshnessSnapshot;
  observe(callback: (snapshot: FreshnessSnapshot) => void): () => void;
}

interface FreshnessSnapshot {
  asOf: Date;
  peersSynced: PeerStatus[];
  peersMissing: PeerStatus[];
  stalenessMs: number;
  withinBudget: boolean;
}
```

The SDK updates the snapshot on every protocol response and emits to observers.

---

## Health API (Hook 5)

```typescript
interface HealthAPI {
  current(): Promise<HealthSnapshot>;
  observe(callback: (snapshot: HealthSnapshot) => void): () => void;
}

interface HealthSnapshot {
  overall: "healthy" | "degraded" | "broken";
  transports: TransportHealth[];
  degradedReasons: string[];
}
```

Tools render this in their "status" surface (or hide it by default per D-Queue).

---

## Event bus

For events that need attention but aren't tied to a specific call:

```typescript
fabric.events.on("conflict", (event: ConflictEvent) => {
  // present to user or invoke merge helper
});

fabric.events.on("degradation-change", (event: DegradationEvent) => {
  // optionally surface in UI
});

fabric.events.on("agent-retired", (event: AgentRetiredEvent) => {
  // tool may want to clear cached state from that agent
});
```

Events emitted by the SDK:
- `conflict` — Sync detected concurrent writes
- `degradation-change` — transport health changed
- `agent-retired` — an agent was retired
- `grant-revoked` — a Grant in `grants_held` was revoked
- `fif-rotation-needed` — passphrase change requested or envelope expired

---

## Transport management

```typescript
interface TransportManager {
  list(): TransportConfig[];
  add(config: TransportConfig): Promise<void>;
  remove(transportId: string): Promise<void>;
  setPreference(transportId: string, preference: number): Promise<void>;
  current(): TransportConfig | null;  // currently active
  switch(transportId: string): Promise<void>;
}
```

### Transport selection logic

The SDK selects a transport per operation:
1. Try transports in preference order (lower number first)
2. If a transport returns `unavailable` or times out, try the next
3. If all transports fail, queue the operation locally and retry per backoff
4. `local-network` is preferred when reachable (lowest latency, highest sovereignty)

This logic is configurable via `fallbackOnUnavailable: false` in options (some tools want fail-fast).

---

## `fabric-merge-helpers` companion library

Distributed separately, ~20 KB minified.

```typescript
import { mergers } from "@naklitechie/fabric-merge-helpers";

// Append-union: events are unioned, no merge needed (default for append-only logs)
const appendUnion = mergers.appendUnion();

// Last-write-wins-per-key: for key-value maps with vector clock ordering
const lww = mergers.lastWriteWinsPerKey({ keyOf: (event) => event.payload.key });

// Counter: monotonic counter with CRDT semantics
const counter = mergers.counter({ counterField: "delta" });

// Set-add-remove: G-Set or 2P-Set semantics
const setAddRemove = mergers.setAddRemove({
  addKind: "item-added",
  removeKind: "item-removed",
  itemKey: (event) => event.payload.id,
});
```

Used by tools that don't want to roll custom merge logic:
```typescript
fabric.events.on("conflict", async (event) => {
  const resolution = await appendUnion.resolve(event.concurrentEvents);
  await fabric.vault.append({ ...resolution, idempotencyKey: generateUlid() });
  await fabric.sync.conflictAck(event.conflictEventId, resolution.eventId);
});
```

---

## Error types

```typescript
class FabricError extends Error { code: string; retryable: boolean; }

class FIFFormatError extends FabricError {}
class FIFAuthenticationError extends FabricError {}
class FIFEnvelopeUnsupportedError extends FabricError {}

class IdentityLockedError extends FabricError {}

class GrantInvalidError extends FabricError {}
class GrantMissingError extends FabricError {}
class GrantRevokedError extends FabricError {}
class ScopeDeniedError extends FabricError {}
class CaveatUnmetError extends FabricError { unmetCaveats: Caveat[]; }

class IdempotencyConflictError extends FabricError {}
class VaultDecryptionError extends FabricError { eventId: string; }
class SyncConflictError extends FabricError { conflict: ConflictDetail; }
class TransportUnavailableError extends FabricError {}
class HumanApprovalRequiredError extends FabricError { pendingOperationId: string; }
class VersionMismatchError extends FabricError {}
```

All errors carry `code` (matching the protocol error codes) and `retryable`.

---

## Browser compatibility

Required APIs (consumer's browser must support these):
- WebCrypto Subtle API with Ed25519, HKDF-SHA256, AES-GCM, ChaCha20-Poly1305
  - WebCrypto's ChaCha20-Poly1305 support is variable; we use XChaCha20-Poly1305 via @noble/ciphers anyway, so this isn't a blocker
- File System Access API (recommended; falls back to download/upload flow for FIF)
- IndexedDB (for queue persistence; fallback to OPFS or memory)
- EventSource (for SSE subscriptions)
- Fetch API
- BroadcastChannel (for multi-tab coordination)

Minimum browser versions:
- Chrome/Edge 119+
- Safari 17+
- Firefox 121+

The SDK warns on capability gaps; tools may degrade or refuse to load.

---

## Multi-tab coordination

When the same tool runs in multiple tabs:
- One tab is elected leader via BroadcastChannel + Web Locks API
- Leader manages the operation queue and transport connections
- Followers send operations to the leader via BroadcastChannel
- On leader tab close, election re-runs

This avoids duplicate operations and conflicting queue state.

---

## Conformance with the protocol

The SDK MUST:
1. Generate ULID idempotency keys for all state-changing operations
2. Send `X-Fabric-Version: naklimesh/1.0`
3. Honor `freshness.staleness_ms` and propagate to `FreshnessAPI`
4. Persist idempotency keys client-side for replay safety
5. Refuse operations with `IdentityLockedError` after `lock()`
6. Verify FIF MAC before trusting any inner content
7. Encrypt payloads client-side; never send plaintext
8. Support all caveat types in the v1.0 catalog (verification helpers)
9. Handle 202 Accepted for `requires-human-approval` operations
10. Emit conflict events with structured detail

The SDK SHOULD:
- Cache discharge macaroons per Grant
- Use Web Locks for inter-tab leader election
- Compress event batches for Sync when transports advertise support

---

## Versioning and stability

The SDK follows semver:
- `1.0.x` — bug fixes, no API changes
- `1.x.0` — additive API changes, backward compatible
- `2.0.0` — breaking changes

The wire protocol version is independent of the SDK version. SDK v1.x talks to protocol v1.x; SDK v2.0 may require protocol v2.0.

---

## Out of scope for v1.0

- React/Vue/Svelte bindings (these are tool-side wrappers, not SDK responsibilities)
- TypeScript decorators or build-time codegen
- Server-side rendering (SDK is browser-side only; Go SDK handles server-side)
- WebSocket transport (SSE is the v1.0 push mechanism)
- WebAuthn/passkey integration with FIF (deferred to v1.x)

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md`
- Decision log: `private-mesh-decisions-v0.7.md`
- @noble/ciphers: https://github.com/paulmillr/noble-ciphers (XChaCha20-Poly1305)
- noble-curves: https://github.com/paulmillr/noble-curves (Ed25519 fallback)
- File System Access API: https://wicg.github.io/file-system-access/
