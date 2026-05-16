# Fabric Protocol Specification

**Document:** `fabric-spec-001-v1.0.md`
**Status:** v1.0 draft, normative
**Companion to:** `private-mesh-vision-001-v0.7.md`, `private-mesh-decisions-v0.7.md`
**Audience:** Implementers of any Fabric consumer, SDK, or transport.

The Fabric Protocol is the contract. Everything else — SDKs, transports, tools, agents — implements or consumes it. This spec is what's load-bearing; SDKs are convenience wrappers around it.

If something here conflicts with vision or decisions docs, **this document wins** for v1.0 wire format and semantics. Vision and decisions inform; the protocol is the contract.

---

## Scope

This document specifies:
- Wire format (HTTP/JSON request/response shapes)
- Capability token format (macaroons, with caveats)
- Fabric Identity File (FIF) format
- Seven primitives' protocol endpoints
- The six failure-model hooks at the protocol level
- Conformance requirements

This document does NOT specify:
- Any specific SDK language or API surface
- Any specific transport implementation's storage choice
- UI affordances for human operators
- Agent provisioning UX

---

## Terminology

- **Fabric Protocol** — the spec in this document. Wire format and semantics.
- **Fabric SDK** — a language-specific library that implements the protocol. JavaScript and Go reference SDKs ship in v1.0.
- **Transport** — a process that speaks the protocol on behalf of a peer's storage. Three reference transports ship in v1.0: Go Hub binary, Cloudflare Worker, Local Network (mDNS).
- **Consumer** — any process that uses the protocol (via SDK or directly). Browser tools, native binaries, CLIs, daemons, agents, devices.
- **Tool** — a consumer with a user interface (human or agent-facing).
- **Principal** — an identity that holds Grants. Humans, agents, and device subkeys are all principals.
- **Grant** — a capability token (macaroon) authorizing a principal to perform a class of operations within a scope, subject to caveats, until expiry.
- **Vault** — encrypted, content-addressed event storage.
- **History** — append-only hash-chained log; a specialization of Vault.
- **FIF** — Fabric Identity File. Encrypted bundle held by a principal containing root key material, transport configurations, and held grants.
- **Caveat** — a constraint on a Grant. Macaroon caveats can be first-party (verifiable locally) or third-party (verified via discharge from a named verifier).

---

## Protocol versioning

The wire protocol carries a version string in every request and response. v1.0 uses the string `"naklimesh/1.0"`.

Versioning rules:
- Implementations MUST refuse requests with mismatched major versions.
- v1.x adds endpoints and caveat types without breaking v1.0 clients.
- v2.0 may break compatibility; migration path is out of scope for this doc.

A peer's reported protocol version is included in every response (`X-Fabric-Version` header) and in the `fabric_version` field of the FIF.

---

## Wire format

All requests and responses are HTTP/1.1 or HTTP/2 with JSON bodies. UTF-8 throughout.

### Common request headers

```
POST /fabric/v1/<endpoint> HTTP/1.1
Host: <transport-host>
Content-Type: application/json
X-Fabric-Version: naklimesh/1.0
X-Fabric-Grant: <base64-macaroon>
X-Fabric-Idempotency-Key: <ulid>
X-Fabric-Request-Id: <ulid>
```

- `X-Fabric-Grant` — required for all endpoints except `/fabric/v1/health` and `/fabric/v1/discover`. The macaroon authorizing this request.
- `X-Fabric-Idempotency-Key` — required for all state-changing operations (writes, Bridge calls). ULID format. See "Idempotency."
- `X-Fabric-Request-Id` — required on all requests. Identifies this specific request in logs.

### Common response shape

Successful responses:
```json
{
  "ok": true,
  "data": { ... endpoint-specific ... },
  "freshness": {
    "as_of": "2026-05-15T18:30:00Z",
    "peers_synced": ["device-abc", "device-xyz"],
    "peers_missing": ["agent-claude-code-001"],
    "staleness_ms": 0
  }
}
```

Error responses:
```json
{
  "ok": false,
  "error": {
    "code": "grant_invalid",
    "message": "Macaroon signature verification failed",
    "retryable": false
  }
}
```

Standard error codes:
- `grant_invalid` — macaroon signature, expiry, or caveats failed verification (non-retryable)
- `grant_missing` — no Grant supplied (non-retryable)
- `grant_revoked` — Grant present on revocation list (non-retryable)
- `scope_denied` — Grant valid but operation outside its scope (non-retryable)
- `caveat_unmet` — Grant valid, scope ok, but a caveat condition is not satisfied (sometimes retryable; see caveat semantics)
- `idempotency_conflict` — idempotency key reused with different payload (non-retryable)
- `not_found` — addressed resource does not exist (non-retryable)
- `conflict` — concurrent write detected; see conflict semantics (non-retryable from this peer, may retry after merge)
- `unavailable` — transport temporarily cannot serve; queue and retry (retryable)
- `partition` — transport reachable but its upstream is not; degraded mode (retryable)
- `version_mismatch` — protocol version not supported (non-retryable)
- `rate_limited` — caveat rate limit hit (retryable after cooldown)
- `human_approval_required` — operation queued pending out-of-band human approval (non-retryable from this call; result becomes available later via the approval endpoint)

The `retryable` boolean is authoritative. Consumers MUST respect it; SDKs MUST not retry non-retryable errors.

### HTTP status codes

- `200 OK` — successful response (also when `ok: false` for application errors that are not transport failures)
- `202 Accepted` — operation accepted and queued for asynchronous processing (Bridge with `requires-human-approval`, etc.)
- `400 Bad Request` — malformed request body or headers
- `401 Unauthorized` — Grant missing or invalid
- `403 Forbidden` — Grant valid but scope denied or caveat unmet
- `404 Not Found` — endpoint or addressed resource not found
- `409 Conflict` — idempotency or concurrent-write conflict
- `429 Too Many Requests` — rate limit caveat hit
- `503 Service Unavailable` — transport degraded; retry later

Transports MUST set the appropriate HTTP status AND the `ok` boolean. Both signals carry the same meaning; consumers MAY use either.

### CORS

Transports MUST accept requests from any origin. Specifically, transports MUST return:
```
Access-Control-Allow-Origin: *
Access-Control-Allow-Headers: Content-Type, X-Fabric-Grant, X-Fabric-Idempotency-Key, X-Fabric-Request-Id, X-Fabric-Version
Access-Control-Allow-Methods: GET, POST, OPTIONS
Access-Control-Expose-Headers: X-Fabric-Version
```

Origin headers MUST NOT be used as an authorization mechanism. Grant verification is the only authorization. This is enforced at every transport implementation; the conformance suite tests it.

---

## Fabric Identity File (FIF)

The FIF is a user-held file that contains a principal's root key material, transport configurations, and held grants. It is decrypted on demand by the consumer.

### Outer structure (envelope)

```
fabric-identity-file := envelope-header || envelope-body || envelope-mac
```

`envelope-header` is a JSON-with-known-shape, length-prefixed (4 bytes big-endian):

```json
{
  "format": "fif/1.0",
  "envelope_type": "passphrase-only",
  "envelope_params": {
    "kdf": "argon2id",
    "kdf_params": {
      "m_cost": 65536,
      "t_cost": 3,
      "parallelism": 4
    },
    "salt": "<base64, 16 bytes>",
    "nonce": "<base64, 24 bytes>"
  }
}
```

`envelope_type` is one of:
- `passphrase-only` (REQUIRED in v1.0)
- `shamir-shares` (RESERVED; v1.x)
- `device-quorum` (RESERVED; v1.x)
- `social-recovery` (RESERVED; v1.x)

Implementations MUST refuse FIFs with unknown `envelope_type`. v1.x readers MAY support v1.0 FIFs (forward compatible at the envelope layer).

`envelope-body` is the inner FIF encrypted with XChaCha20-Poly1305 using a key derived from the envelope parameters (passphrase + KDF for `passphrase-only`).

`envelope-mac` is the Poly1305 tag (16 bytes) covering the entire envelope-header and envelope-body. This is the AEAD authentication tag.

### Inner FIF structure

After decryption:

```json
{
  "format": "fif-inner/1.0",
  "principal": {
    "type": "human",
    "id": "<ulid>",
    "display_name": "Bhai",
    "created_at": "2026-05-15T18:00:00Z"
  },
  "root_keypair": {
    "algorithm": "ed25519",
    "public_key": "<base64>",
    "private_key": "<base64>"
  },
  "device_subkeys": [
    {
      "device_id": "<ulid>",
      "device_name": "M4 Pro",
      "algorithm": "ed25519",
      "public_key": "<base64>",
      "private_key": "<base64>",
      "enrolled_at": "2026-05-15T18:05:00Z"
    }
  ],
  "agent_identities": [
    {
      "agent_id": "<ulid>",
      "agent_name": "claude-code-tax-prep",
      "vendor": "anthropic",
      "algorithm": "ed25519",
      "public_key": "<base64>",
      "private_key": "<base64>",
      "provisioned_at": "2026-05-15T19:00:00Z",
      "expires_at": "2026-06-15T19:00:00Z"
    }
  ],
  "transports": [
    {
      "id": "<ulid>",
      "type": "hub",
      "url": "https://hub.bhai.example.com",
      "public_key": "<base64>",
      "preference": 1
    },
    {
      "id": "<ulid>",
      "type": "cf-worker",
      "url": "https://fabric.bhai.workers.dev",
      "public_key": "<base64>",
      "preference": 2
    },
    {
      "id": "<ulid>",
      "type": "local-network",
      "service_name": "_nakli-fabric._tcp",
      "preference": 0
    }
  ],
  "grants_held": [
    {
      "grant_id": "<ulid>",
      "macaroon": "<base64>",
      "issued_at": "2026-05-15T18:00:00Z",
      "expires_at": "2026-06-15T18:00:00Z",
      "issued_by": "<principal-id>",
      "scope_summary": "vault:read+write namespace=list"
    }
  ],
  "bridge_credentials": [
    {
      "service": "courtlistener",
      "credential_type": "api_key",
      "credential_value": "<encrypted-string>",
      "scope_summary": "read court records"
    }
  ],
  "recent_state_cache": {
    "vault_heads": {
      "<namespace>": "<latest-event-hash>"
    },
    "history_heads": {
      "<stream-id>": "<latest-event-hash>"
    }
  }
}
```

Notes:
- `principal.type` is `"human"`, `"agent"`, or `"device"`.
- `device_subkeys` are for the human's devices. Each device has its own keypair derived from the root, used for signing operations from that device.
- `agent_identities` are for agents provisioned by this principal. Each agent has its own keypair, distinct from devices. Agent FIFs (if the agent holds its own FIF) have `principal.type = "agent"`.
- `transports` is ordered by preference (lower number = higher preference). Consumers SHOULD try transports in preference order.
- `grants_held` is the principal's held grants. The cache is opportunistic; authoritative grants live in Vault.
- `bridge_credentials` are BYOK credentials for external services. Encrypted with a separate key derived from the root (so a compromised in-memory Vault key does not expose Bridge credentials).
- `recent_state_cache` is opportunistic; not authoritative.

### FIF lifecycle operations

The protocol defines these FIF-level operations (handled by the consumer/SDK, not the transport):

- `fif_create(passphrase) -> FIF` — generate root key, write minimal FIF
- `fif_unlock(fif_bytes, passphrase) -> InnerFIF` — decrypt and authenticate
- `fif_rotate_envelope(inner, new_envelope_type, new_params) -> FIF` — re-encrypt under new envelope; inner is unchanged
- `fif_enroll_device(inner, device_name) -> InnerFIF'` — add device subkey
- `fif_provision_agent(inner, agent_spec) -> InnerFIF'` — add agent identity
- `fif_retire_agent(inner, agent_id) -> InnerFIF'` — mark agent retired (also writes a retirement event to History; see Agents)

FIF mutations are atomic: write a new FIF file alongside the old one, fsync, rename. Never partial writes.

---

## Capability tokens (Grants / Macaroons)

Grants are macaroons. The protocol uses libmacaroon-style serialization with these specifics:

### Macaroon structure

```
macaroon := location || identifier || caveats* || signature
```

Wire serialization is the libmacaroon v2 binary format, base64-encoded for the `X-Fabric-Grant` header.

- `location` — the transport host or logical address this Grant is bound to (or `*` for transport-agnostic Grants)
- `identifier` — a JSON object (base64-encoded) with this shape:
  ```json
  {
    "grant_id": "<ulid>",
    "issued_at": "<rfc3339>",
    "issued_by_principal": "<principal-id>",
    "issued_by_keypair": "<base64-pub-key>",
    "parent_grant_id": "<ulid-or-null>",
    "scope": {
      "primitive": "vault | history | bridge | llm | sync",
      "namespace": "<namespace-or-*>",
      "operations": ["read", "write", "subscribe", ...]
    }
  }
  ```
- `caveats` — first-party and third-party caveats (see "Caveat catalog")
- `signature` — HMAC-SHA256 chain over identifier and caveats

### Caveat catalog (v1.0)

First-party caveats (verified locally at the transport):

- `time < <rfc3339>` — Grant expires at this time
- `time > <rfc3339>` — Grant not valid before this time
- `principal-type in [human, agent, device]` — restricts who can use the Grant
- `agent-id == <ulid>` — restricts to a specific agent
- `device-id == <ulid>` — restricts to a specific device
- `operation in [read, write, ...]` — restricts allowed operations (intersected with scope.operations)
- `namespace == <string>` — restricts to a specific namespace (intersected with scope.namespace)
- `rate <= N per <window>` — rate limit, where window is one of `second`, `minute`, `hour`, `day`
- `max-amount <= <integer> <currency>` — for Bridge calls with financial side effects
- `only-domain in [<domain>, ...]` — for Bridge calls; restricts allowed call destinations
- `requires-human-approval` — for Bridge calls and other side-effect operations; queues the operation pending approval (HTTP 202)
- `nondelegatable` — Grant cannot be used to mint child Grants
- `idempotency-required` — operation MUST carry an idempotency key (this caveat is implicit on all Bridge calls; explicit on other operations as needed)

Third-party caveats (verified via discharge):

- `discharge-from <verifier-url>` — operation requires a discharge macaroon from the named verifier; used for revocation lists (the verifier is the History stream that holds revocations)

### Grant verification

A transport MUST verify, in order:

1. **Macaroon signature** chain (using the issuer's public key from the FIF or from a known signing key set)
2. **Caveat satisfaction** — every first-party caveat must hold; every third-party caveat must have a valid discharge macaroon present in the request
3. **Scope match** — the request's primitive, namespace, and operation must be a subset of the Grant's scope
4. **Idempotency-key presence** when required

If any check fails, transport responds with `grant_invalid`, `scope_denied`, or `caveat_unmet` as appropriate.

### Grant minting and delegation

A consumer (typically the operator's CLI or a UI) mints a Grant by:
1. Constructing the identifier
2. Computing the initial signature with HMAC-SHA256 of the identifier under the issuer's root key
3. Adding caveats one at a time, each updating the signature

Delegation:
- A consumer holding Grant G can mint a Grant G' that is G with additional caveats appended
- G' is strictly narrower than G
- A consumer holding G CANNOT mint a Grant G' with fewer caveats than G; macaroon construction does not allow this
- Therefore: agents can only attenuate, never expand

This is the cryptographic enforcement that backs Principle 11.

### Grant revocation

Two modes (per D4):

**Opportunistic refresh** — for Grants the principal holds on their own devices.
- Grant has a long-lived expiry (default 30 days)
- Consumer refreshes opportunistically by minting a new Grant before the old expires
- Under partition, the existing Grant works until expiry
- No revocation list check

**Revocation list** — for Grants delegated to other parties (typically: agents and other humans).
- Grant carries a `discharge-from <history-stream>` third-party caveat
- The History stream holds revocation events
- Transport checks the discharge macaroon at use time
- Stale discharge is acceptable per the staleness budget (default: 24 hours); operations succeed with `freshness.staleness_ms` reflecting the lag
- Revocation event in History causes the discharge to refuse renewal on next request

Transports SHOULD cache discharge macaroons with a TTL equal to the staleness budget.

---

## The seven primitives — protocol endpoints

All endpoints under `/fabric/v1/`. All require `X-Fabric-Grant` except where noted.

### Identity

Identity is largely client-side (FIF operations). The protocol exposes:

#### `GET /fabric/v1/identity/principal`
Returns the principal-id of the Grant holder.
- Auth: any valid Grant
- Response: `{ ok: true, data: { principal_id, principal_type, public_key } }`

#### `POST /fabric/v1/identity/pair/initiate`
Initiate a pairing flow. The existing device calls this to generate a pairing capability.
- Auth: Grant with scope `identity:pair`
- Request: `{ pairing_method: "qr"|"code"|"link", expires_in_seconds: 600 }`
- Response: `{ ok: true, data: { pairing_token, rendezvous_url, expires_at, qr_payload, numeric_code, magic_link } }`

#### `POST /fabric/v1/identity/pair/complete`
The new device redeems a pairing token to receive a device subkey enrollment.
- Auth: none (the pairing_token is the auth)
- Request: `{ pairing_token, new_device_public_key, new_device_name }`
- Response: `{ ok: true, data: { device_id, enrollment_grant, transport_configs } }`

#### `POST /fabric/v1/identity/agents/provision`
Mint an agent identity.
- Auth: Grant with scope `identity:agent-provision`
- Request: `{ agent_name, vendor, public_key, expires_at }`
- Response: `{ ok: true, data: { agent_id } }`

#### `POST /fabric/v1/identity/agents/retire`
Retire an agent (writes to History; subsequent operations under the agent's keys fail with `grant_invalid`).
- Auth: Grant with scope `identity:agent-retire`
- Request: `{ agent_id, reason }`
- Response: `{ ok: true, data: { retirement_event_id } }`

### Grant

#### `POST /fabric/v1/grant/mint`
Mint a Grant (typically the operator's flow). Client-side construction is preferred; this endpoint exists for transports that issue Grants on the operator's behalf.
- Auth: Grant with scope `grant:mint` (or any Grant for delegation if the new Grant is strictly narrower)
- Request: `{ scope, caveats, expires_at, recipient_principal_id }`
- Response: `{ ok: true, data: { macaroon, grant_id } }`

#### `POST /fabric/v1/grant/verify`
Verify a Grant against a hypothetical operation. Useful for tools that want to surface "what could this agent do."
- Auth: any valid Grant
- Request: `{ macaroon, hypothetical_operation: { primitive, namespace, operation } }`
- Response: `{ ok: true, data: { would_succeed: bool, reasons: [...] } }`

#### `POST /fabric/v1/grant/revoke`
Write a revocation event to the revocation History stream for this Grant's discharge verifier.
- Auth: Grant with scope `grant:revoke`
- Request: `{ grant_id, reason }`
- Response: `{ ok: true, data: { revocation_event_id } }`

### Vault

#### `POST /fabric/v1/vault/append`
Append an event to a Vault stream.
- Auth: Grant with scope `vault:write` in the target namespace
- Request:
  ```json
  {
    "namespace": "<string>",
    "stream_id": "<ulid>",
    "event": {
      "kind": "<application-defined>",
      "payload_ciphertext": "<base64>",
      "payload_metadata": { ... },
      "causal_dependencies": ["<event-id>", ...],
      "vector_clock": { "<device-id>": <integer>, ... }
    }
  }
  ```
- Idempotency: required (`X-Fabric-Idempotency-Key`)
- Response: `{ ok: true, data: { event_id, sequence_number } }`

#### `GET /fabric/v1/vault/stream/<namespace>/<stream_id>`
Read events from a Vault stream.
- Auth: Grant with scope `vault:read` in namespace
- Query params: `since=<event-id>`, `limit=<integer, max 1000>`
- Response: `{ ok: true, data: { events: [...], more: bool } }`

#### `GET /fabric/v1/vault/streams/<namespace>`
List streams in a namespace.
- Auth: Grant with scope `vault:read` in namespace
- Response: `{ ok: true, data: { streams: [{ stream_id, latest_event_id, event_count }] } }`

#### `POST /fabric/v1/vault/subscribe`
Subscribe to a stream via SSE for real-time delivery.
- Auth: Grant with scope `vault:subscribe` in namespace
- Request: `{ namespace, stream_id, since_event_id? }`
- Response: SSE stream of events

### History

History is a specialization of Vault with extra structure. The endpoints are:

#### `POST /fabric/v1/history/append`
Append to a History stream (writes both as a Vault event AND with hash-chain metadata).
- Auth: Grant with scope `history:write` for the stream
- Request:
  ```json
  {
    "stream_id": "<ulid>",
    "event": {
      "kind": "<application-defined>",
      "payload_ciphertext": "<base64>",
      "payload_metadata": { ... },
      "previous_event_hash": "<base64>"
    }
  }
  ```
- Idempotency: required
- Response: `{ ok: true, data: { event_id, event_hash, sequence_number } }`

The transport MUST verify `previous_event_hash` matches the current head before appending. Mismatch results in `conflict`.

#### `GET /fabric/v1/history/stream/<stream_id>`
Read History events with hash-chain verification.
- Auth: Grant with scope `history:read` for the stream
- Response: includes hashes for verification

#### `GET /fabric/v1/history/verify/<stream_id>`
Walk the hash chain and return verification status.
- Auth: Grant with scope `history:read` for the stream
- Response: `{ ok: true, data: { verified: bool, length: integer, head_hash } }`

### Sync

Sync moves events between peers. The fabric guarantees ordered delivery.

#### `POST /fabric/v1/sync/push`
Push events to a remote peer.
- Auth: Grant with scope `sync:push` for the target peer
- Request: `{ peer_id, events: [...], from_event_id? }`
- Response: `{ ok: true, data: { accepted_events: [...], rejected_events: [...] } }`

#### `GET /fabric/v1/sync/pull`
Pull events from a remote peer.
- Auth: Grant with scope `sync:pull`
- Query: `peer_id=<id>`, `since=<event-id>`, `limit=<integer>`
- Response: events with causal-ordering metadata

#### `GET /fabric/v1/sync/peers`
List known peers.
- Auth: Grant with scope `sync:read`
- Response: `{ ok: true, data: { peers: [{ peer_id, last_seen, freshness_ms }] } }`

#### `POST /fabric/v1/sync/conflict-ack`
Acknowledge that a conflict has been observed (for tools that resolve them).
- Auth: Grant with scope `sync:write`
- Request: `{ conflict_event_id, resolution_event_id }`
- Response: `{ ok: true }`

### LLM

#### `POST /fabric/v1/llm/complete`
Request a completion. Routes per the user's configured order (anchor → browser-local → remote BYOK).
- Auth: Grant with scope `llm:invoke`, optionally with `max-amount` caveat for cost
- Request:
  ```json
  {
    "messages": [{ "role": "user|assistant|system", "content": "..." }],
    "model_capabilities": {
      "min_context_window": 32000,
      "needs_vision": false,
      "needs_function_calling": true
    },
    "preferred_route": "local|browser-local|remote|auto",
    "max_cost_cents": 100
  }
  ```
- Idempotency: required (deduplicates retries)
- Response: `{ ok: true, data: { content, route_taken, cost_cents, tokens } }`

#### `GET /fabric/v1/llm/routes`
Report available routes and their status.
- Auth: Grant with scope `llm:read`
- Response: `{ ok: true, data: { routes: [{ name, available, latency_ms, supported_capabilities }] } }`

### Bridge

#### `POST /fabric/v1/bridge/call`
Make a call to an external service.
- Auth: Grant with scope `bridge:call`, with appropriate caveats (`only-domain`, `max-amount`, etc.)
- Request:
  ```json
  {
    "adapter": "<adapter-name>",
    "operation": "<adapter-operation>",
    "params": { ... },
    "dry_run": false
  }
  ```
- Idempotency: required
- Response: `{ ok: true, data: { result, ... } }`
- If Grant carries `requires-human-approval`: HTTP 202, response includes a pending operation ID.

#### `GET /fabric/v1/bridge/adapters`
List installed Bridge adapters.
- Auth: Grant with scope `bridge:read`
- Response: list of adapters with their capabilities.

#### `POST /fabric/v1/bridge/approve`
Approve a pending Bridge operation.
- Auth: Grant with scope `bridge:approve`, principal-type must be `human`
- Request: `{ pending_operation_id, approve: bool, reason? }`
- Response: result of the executed operation, or rejection record

---

## Failure-model hooks at the protocol level

The six hooks (D-Failure) are realized as follows:

### Hook 1: Operation queue
At the protocol level: every state-changing endpoint MUST be idempotent (via `X-Fabric-Idempotency-Key`). Consumers queue operations locally; SDKs replay against transports until success. This is a consumer/SDK concern; the protocol's role is making replay safe.

### Hook 2: Causal ordering metadata
Every Vault and History event carries:
- `vector_clock` — map of device-id to sequence number, expressing causal ancestry
- `causal_dependencies` — explicit list of event IDs this event causally depends on

Consumers compute these client-side; transports preserve them in storage and return them in reads.

### Hook 3: Bounded staleness visibility
Every response carries a `freshness` object (see Common Response Shape). Specifically:
- `as_of` — when this transport last successfully synced with each named peer
- `peers_synced` — peers that have synced within the staleness budget
- `peers_missing` — peers that have not
- `staleness_ms` — max staleness across all peers known to the transport

Consumers expose this to tools per Hook 5.

### Hook 4: Idempotency keys
Every state-changing endpoint REQUIRES `X-Fabric-Idempotency-Key`. ULID format. Transports MUST:
- Store the idempotency key alongside the operation's result for at least 24 hours
- If the same key arrives with the same payload: return the original result (HTTP 200)
- If the same key arrives with a DIFFERENT payload: return `idempotency_conflict` (HTTP 409)
- This is mandatory for all consumers; especially load-bearing for agent operations (D-Agents)

### Hook 5: Graceful degradation surface
Transports expose health and degradation state:

#### `GET /fabric/v1/health`
- Auth: none
- Response:
  ```json
  {
    "ok": true,
    "data": {
      "transport_id": "<ulid>",
      "version": "naklimesh/1.0",
      "uptime_seconds": 12345,
      "degraded": false,
      "degraded_reasons": [],
      "peer_health": { ... },
      "queue_depth": 0
    }
  }
  ```

### Hook 6: Conflict surface
Sync emits `conflict` errors with structured metadata:
```json
{
  "ok": false,
  "error": {
    "code": "conflict",
    "message": "Concurrent writes detected in stream X",
    "retryable": false,
    "conflict": {
      "stream_id": "<ulid>",
      "concurrent_events": ["<event-id-a>", "<event-id-b>"],
      "common_ancestor": "<event-id>"
    }
  }
}
```
Tools handle conflicts using `fabric-merge-helpers` library or custom logic.

---

## Discovery

#### `GET /fabric/v1/discover`
Returns transport capability and topology information.
- Auth: none
- Response:
  ```json
  {
    "ok": true,
    "data": {
      "transport_type": "hub|cf-worker|local-network",
      "transport_id": "<ulid>",
      "version": "naklimesh/1.0",
      "supported_primitives": ["vault", "history", "sync", "grant", "identity", "llm", "bridge"],
      "supported_caveats": ["time", "rate", "max-amount", "only-domain", "requires-human-approval", ...],
      "max_event_size_bytes": 1048576,
      "max_idempotency_window_seconds": 86400
    }
  }
  ```

This endpoint enables consumers to verify capabilities before attempting operations.

---

## Pairing protocol detail

Pairing has three v1.0 modes (D5-pairing). All three deliver the same underlying pairing capability.

### Common pairing flow

1. Existing device calls `POST /fabric/v1/identity/pair/initiate` → receives pairing artifacts (QR payload, numeric code, magic link)
2. The artifact is conveyed to the new device by the user (scan, type, share-link)
3. New device generates a keypair locally
4. New device calls `POST /fabric/v1/identity/pair/complete` with the pairing token + its public key
5. Transport returns the device_id, an initial Grant for the new device, and transport configs
6. New device prompts user for FIF passphrase; decrypts an out-of-band-transmitted FIF (or downloads it via the initial Grant)
7. New device adds its subkey to the FIF and writes back

### Local-only mode

When both devices are on the same local network, the magic link points at a local IP. No external service is involved. mDNS discovery may be used to find the source device's local IP automatically.

### QR payload format

```json
{
  "fabric_pair": "1.0",
  "pairing_token": "<ulid>",
  "rendezvous_url": "https://hub.example.com/fabric/v1/identity/pair/complete",
  "expires_at": "<rfc3339>",
  "fif_integrity_commitment": "<base64>"
}
```

Encoded as Base32 (QR-friendly) before being rendered to QR.

### Numeric code

6-digit numeric code. Lookup table mapping code → pairing_token, stored at the transport for the duration of the pairing window.

### Magic link

`https://<transport>/fabric/v1/identity/pair/complete?token=<pairing_token>`

For local-only mode: `http://<local-ip>:<port>/fabric/v1/identity/pair/complete?token=<pairing_token>`

---

## Idempotency

ULID format for all idempotency keys: `<48-bit-timestamp><80-bit-randomness>`, Crockford base32.

Transports MUST:
- Persist idempotency keys with their corresponding operation result for ≥ 24 hours
- Treat duplicate keys with identical payloads as success replay (HTTP 200, original response)
- Treat duplicate keys with different payloads as `idempotency_conflict` (HTTP 409)
- Per-Grant key namespacing (Grant A's idempotency keys do not collide with Grant B's)

Consumers SHOULD:
- Generate one idempotency key per logical operation
- Reuse the same key on retry
- Generate a fresh key only when the user (or agent) explicitly intends a new operation

---

## Encryption

All event payloads are encrypted before being sent to transports. Transports see ciphertext. Always.

### Payload encryption

- Algorithm: XChaCha20-Poly1305
- Key derivation: per-namespace symmetric key derived from root key + namespace name via HKDF-SHA256
- Nonce: 24 bytes, random per-event
- AAD: namespace || stream_id || event_id || vector_clock_hash

### Bridge credential encryption

- Bridge credentials are double-encrypted: once with a Bridge-specific key (derived separately from root), once as part of the FIF envelope
- This means an in-memory compromise of the Vault key does not expose Bridge credentials

### Key rotation

- Root key rotation is a manual operation; v1.0 does not specify a rotation flow (deferred to v1.x)
- Per-namespace keys are derived deterministically from root + namespace name, so they "rotate" only when root rotates

---

## Conformance

The conformance test suite (`nakli-conformance`) MUST verify, for any claimed implementation:

### Wire format conformance
1. Reject malformed JSON with HTTP 400
2. Reject unknown protocol version with `version_mismatch`
3. Return CORS headers per spec
4. Return `freshness` object in all responses
5. Include `X-Fabric-Version` in all responses

### Grant conformance
6. Reject requests without `X-Fabric-Grant` (except `/health` and `/discover`)
7. Reject malformed macaroons
8. Reject expired Grants
9. Reject Grants whose scope doesn't match the operation
10. Reject Grants with unmet caveats
11. Honor `rate` caveat (test with bursts)
12. Honor `max-amount` caveat (Bridge calls)
13. Honor `only-domain` caveat (Bridge calls)
14. Honor `requires-human-approval` (return 202, pending operation accessible)
15. Refuse delegation that would widen scope
16. Verify third-party discharge for revocation

### Idempotency conformance
17. Replay same key + same payload → original response
18. Replay same key + different payload → 409 conflict
19. Persist keys ≥ 24 hours
20. Reject state-changing operations missing idempotency key

### Vault/History conformance
21. Reject Vault writes to namespace outside Grant scope
22. Reject History append with mismatched previous_event_hash → conflict
23. Return events in causal order on read
24. Verify hash chain on `/history/verify`

### Failure-model conformance
25. Return `freshness` with `staleness_ms` reflecting actual peer sync lag
26. `degraded: true` in `/health` when transport cannot reach configured peers
27. Conflict events include `concurrent_events` and `common_ancestor`

### Adversarial conformance (D-Agents)
28. Reject Grant signature forgery
29. Reject Grant replay across different recipient principals (when Grant is bound)
30. Reject agent attempting to use a Grant after agent retirement
31. Reject Bridge call without idempotency key
32. Reject delegation that omits caveats present in parent

Implementations passing all 32 tests are conformant for v1.0.

---

## Out of scope for v1.0 (referenced by v1.x)

- Anchor-cluster fabric (multiple anchors as a single logical transport)
- Federated identity (multiple humans on one fabric, each with their own root)
- Cross-fabric capability delegation (Grant minted in fabric A used in fabric B)
- BLE / NFC pairing (mechanism only — protocol unchanged)
- Distributed FIF envelope types (`shamir-shares`, `device-quorum`, `social-recovery`)
- Anomaly detection on agent operations
- Real-time collaborative editing primitives (OT, RGA, etc.)

---

## Open issues for spec review

These are noted for the v1.0 review pass, not blocking implementation:

- **Bridge adapter discovery format** — should adapters be self-describing JSON, or a separate adapter registry? Current spec assumes self-describing.
- **Pairing token entropy** — 128 bits seems sufficient; explicit decision needed.
- **Event size limit** — 1 MB chosen as a reasonable upper bound; revisit if tools need more.
- **Discharge macaroon caching** — TTL equals staleness budget by default; consumers may override per-Grant.

---

## References

- Macaroon paper: Birgisson et al., "Macaroons: Cookies with Contextual Caveats for Decentralized Authorization in the Cloud" (NDSS 2014)
- libmacaroon: https://github.com/rescrv/libmacaroons
- XChaCha20-Poly1305: RFC 8439 + XChaCha extension (Bernstein draft)
- Argon2id: RFC 9106
- ULID: https://github.com/ulid/spec
- ed25519: RFC 8032
- HKDF-SHA256: RFC 5869
