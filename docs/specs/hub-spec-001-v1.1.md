# nakli-hub Specification

**Document:** `hub-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `hub-spec-001-v1.0.md` — adds explicit dependency choices (SQLite driver, HTTP routing, mDNS library) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md`, `fabric-sdk-go-spec-001-v1.0.md`
**Audience:** Implementers of `nakli-hub`; operators deploying it.

`nakli-hub` is the canonical self-hosted Fabric transport. It implements the full Fabric Protocol server-side, runs on the anchor (or VPS, or Pi, or any always-on machine), and stores ciphertext at rest. Single binary, single config file, no external dependencies.

This is the sovereign-path transport — the user's own box, the user's own data, the user's own network. It is the reference implementation against which other transports (Cloudflare Worker, Local Network) are compared.

---

## Scope

This document specifies:
- Binary layout and packaging
- Configuration file format
- Storage layout (SQLite + filesystem)
- Protocol endpoint implementation requirements
- Macaroon verification implementation
- Discharge macaroon issuance (for revocation)
- Sync between Hubs
- Operational concerns: launchd/systemd, logging, metrics, backup, upgrade
- Security posture
- Conformance with `fabric-spec-001-v1.0.md`

Out of scope:
- Web admin UI (the CLI is the admin surface; the future Gleam integration is operational)
- Distributed Hub clustering (single-node v1.0; multi-node deferred to v1.x)
- Built-in HTTPS (Hub serves HTTP; reverse proxy or NetBird mesh provides TLS)

---

## Dependencies

The Hub binary MUST use the following libraries.

### Required

- **`fabric-sdk-go`** (this monorepo) — for protocol types, macaroon verification, FIF operations. The Hub is a heavy consumer of the SDK.
- **`github.com/mattn/go-sqlite3`** — SQLite driver. Requires cgo; the Hub is a daemon, cgo is fine.
- **`net/http`** with Go 1.22+ pattern matching — for HTTP routing. The Hub's routes map naturally to `mux.HandleFunc("POST /fabric/v1/vault/append", ...)` style. No external router framework needed.
- **`github.com/oklog/ulid/v2`** — ULID generation (via the SDK).

### Recommended

- **`github.com/grandcat/zeroconf`** — mDNS announce/discover. Used if the Hub directly announces itself on the local network (the spec leaves this optional for v1.0; the `nakli-local-bridge` handles mDNS in the canonical path).
- **`github.com/spf13/viper`** or **stdlib `flag` + env parsing** — configuration. The Hub's config is small (~20 keys); the stdlib is enough. Use Viper only if config grows substantially.
- **`golang.org/x/sys/unix`** — for `syscall.Mlock` on the encryption key in memory (security-defense-in-depth, per the security posture section).

### Forbidden

- Web frameworks (Gin, Echo, Fiber, etc.). The Hub's routes are direct and stdlib-shaped. A framework adds dependency surface for no win.
- ORMs (GORM, etc.). The schema is small; raw `database/sql` with prepared statements is clearer and faster.
- Logging frameworks heavier than `log/slog` (stdlib). slog (Go 1.21+) is sufficient.

---

## Binary and distribution

### Binary properties

- Name: `nakli-hub`
- Language: Go 1.22+
- Static binary (no dynamic libraries required)
- Targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- Approximate size: 25–40 MB
- Single binary; configuration via flags, env, or config file
- No runtime dependencies beyond OS

### Distribution channels

- Direct download from `https://naklitechie.com/nakli-hub/<version>/<platform>/`
- GPG-signed (signature file alongside binary)
- SHA-256 checksums published
- Reproducible builds (Go's deterministic builds, cgo disabled)

### Versioning

- Hub binary version is independent of protocol version
- Binary version: `nakli-hub vX.Y.Z`
- Protocol version: `naklimesh/1.0`
- One binary version may speak multiple protocol versions; v1.0 binary speaks only `naklimesh/1.0`

---

## Configuration

### Config file format

TOML, default location `/etc/nakli-hub/config.toml` (Linux) or `~/Library/Application Support/nakli-hub/config.toml` (macOS).

```toml
[hub]
id = "01HMXYZ..."                       # ULID; generated on first run, persisted
listen = "127.0.0.1:7842"               # bind address
data_dir = "/var/lib/nakli-hub"         # writable directory
log_level = "info"                      # silent | error | warn | info | debug

[hub.identity]
# Hub's own keypair for signing freshness metadata, discharge macaroons,
# and peer-to-peer sync auth. NOT the same as user identity.
# Auto-generated on first run; written to data_dir/hub-identity.json
keypair_file = "hub-identity.json"

[storage]
# SQLite for metadata, macaroon cache, queue, peer state
sqlite_db = "fabric.db"
# Filesystem for event payloads (ciphertext blobs)
blobs_dir = "blobs"
max_event_size_bytes = 1048576          # 1 MB per spec
max_blob_count = 10000000               # operator-tunable
fsync_writes = true                     # durable; set false for tests only

[idempotency]
retention_seconds = 86400               # 24h per spec
max_keys_per_grant = 100000

[discharge]
# Revocation list cache TTL
default_ttl_seconds = 3600              # 1h refresh of upstream lists

[sync]
# Inter-Hub sync (when this Hub is part of a multi-anchor setup; v1.0 has 1 peer max)
poll_interval_seconds = 30
peer_timeout_seconds = 10

[peers]
# Other Hubs / Transports this Hub knows about
# Populated by pairing or operator config
# [[peers]] entries are listed at runtime

[health]
freshness_budget_seconds = 86400        # 24h; staleness shows as degraded after this
report_metrics_local = true

[security]
# CORS is always permissive (per protocol)
# Origin headers are not used for auth (per protocol)
rate_limit_unauth_per_minute = 60       # for /health, /discover, /identity/pair/complete
gpg_pubkey_verify_path = "..."          # for verifying updates
```

### Command-line flags

```
nakli-hub serve [--config PATH] [--data-dir PATH] [--listen ADDR]
nakli-hub init [--data-dir PATH]                    # generate config + hub identity
nakli-hub status [--config PATH]                    # print health to stdout
nakli-hub backup [--config PATH] --output PATH      # snapshot SQLite + blobs
nakli-hub restore [--config PATH] --input PATH      # restore from snapshot
nakli-hub conformance [--target URL]                # run conformance suite against another Hub
nakli-hub version
```

### Environment variables

All config keys can be set via env: `NAKLI_HUB_LISTEN=0.0.0.0:7842`, etc. Env overrides config file.

---

## Storage layout

### Directory structure

```
<data_dir>/
├── config.toml                # (sometimes lives in /etc; this is the data side)
├── hub-identity.json          # Hub's keypair (DO NOT LOSE)
├── fabric.db                  # SQLite: metadata, macaroons, queue, peers
├── fabric.db-wal              # SQLite WAL
├── fabric.db-shm              # SQLite shared memory
├── blobs/                     # event payloads (ciphertext)
│   ├── 0a/
│   │   └── 0a3b9c.../event_id.bin
│   ├── ...
│   └── ff/
├── pending/                   # Bridge calls awaiting approval
├── discharges/                # cached discharge macaroons
└── logs/
    └── hub.log
```

Blobs are stored content-addressed: first 2 hex of SHA-256 → subdirectory. This keeps any one directory under ~256 entries.

### SQLite schema

```sql
-- Hub's view of identity
CREATE TABLE hub_identity (
    id TEXT PRIMARY KEY,           -- 'singleton'
    hub_id TEXT NOT NULL,
    public_key BLOB NOT NULL,
    private_key BLOB NOT NULL,     -- encrypted at rest if hub.identity.passphrase set
    created_at TEXT NOT NULL
);

-- Known principals (humans, agents, devices)
CREATE TABLE principals (
    principal_id TEXT PRIMARY KEY,
    principal_type TEXT NOT NULL,  -- 'human' | 'agent' | 'device'
    public_key BLOB NOT NULL,
    parent_principal_id TEXT,       -- for agents: the human who provisioned
    display_name TEXT,
    created_at TEXT NOT NULL,
    retired_at TEXT,                -- NULL if not retired
    retirement_event_id TEXT
);
CREATE INDEX idx_principals_type ON principals(principal_type);
CREATE INDEX idx_principals_parent ON principals(parent_principal_id);

-- Streams (Vault and History)
CREATE TABLE streams (
    stream_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    stream_type TEXT NOT NULL,     -- 'vault' | 'history'
    created_at TEXT NOT NULL,
    head_event_id TEXT,
    head_event_hash BLOB,          -- only for history streams
    event_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, stream_id)
);
CREATE INDEX idx_streams_namespace ON streams(namespace);

-- Events (Vault and History)
CREATE TABLE events (
    event_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    stream_type TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    kind TEXT NOT NULL,
    blob_path TEXT NOT NULL,        -- path under blobs/
    payload_size_bytes INTEGER NOT NULL,
    payload_metadata TEXT,          -- JSON
    causal_dependencies TEXT,       -- JSON array of event_ids
    vector_clock TEXT NOT NULL,     -- JSON map of device_id -> counter
    previous_event_hash BLOB,       -- for history streams
    event_hash BLOB,                -- for history streams
    appended_at TEXT NOT NULL,
    appended_by_principal TEXT NOT NULL,
    appended_by_grant_id TEXT NOT NULL,
    appended_by_device TEXT,
    FOREIGN KEY (namespace, stream_id) REFERENCES streams(namespace, stream_id)
);
CREATE INDEX idx_events_stream ON events(namespace, stream_id, sequence_number);
CREATE INDEX idx_events_appended ON events(appended_at);
CREATE INDEX idx_events_principal ON events(appended_by_principal);

-- Idempotency keys
CREATE TABLE idempotency (
    key TEXT NOT NULL,
    grant_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    payload_hash BLOB NOT NULL,
    response_status INTEGER NOT NULL,
    response_body BLOB,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (key, grant_id)
);
CREATE INDEX idx_idempotency_expires ON idempotency(expires_at);

-- Grant cache (for revocation list lookup and audit)
CREATE TABLE grants_known (
    grant_id TEXT PRIMARY KEY,
    issued_by_principal TEXT NOT NULL,
    recipient_principal TEXT NOT NULL,
    parent_grant_id TEXT,
    scope TEXT NOT NULL,            -- JSON
    caveats TEXT NOT NULL,          -- JSON
    issued_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    revocation_event_id TEXT
);
CREATE INDEX idx_grants_principal ON grants_known(recipient_principal);

-- Peers (other transports this Hub syncs with)
CREATE TABLE peers (
    peer_id TEXT PRIMARY KEY,
    peer_type TEXT NOT NULL,        -- 'hub' | 'cf-worker' | 'local-network'
    url TEXT NOT NULL,
    public_key BLOB NOT NULL,
    last_sync_at TEXT,
    last_seen_at TEXT,
    sync_state TEXT                  -- JSON: cursors, etc.
);

-- Pending Bridge operations awaiting approval
CREATE TABLE pending_bridge (
    pending_id TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL,
    adapter TEXT NOT NULL,
    operation TEXT NOT NULL,
    params TEXT NOT NULL,           -- JSON
    requested_by_principal TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    approve_by TEXT,                -- principal_id that approved
    approved_at TEXT,
    rejected_at TEXT,
    rejected_reason TEXT,
    executed_at TEXT,
    result TEXT                     -- JSON
);

-- Operation log (for audit and Hub-level debugging; separate from History primitive)
CREATE TABLE operation_log (
    op_id TEXT PRIMARY KEY,
    timestamp TEXT NOT NULL,
    grant_id TEXT,
    principal TEXT,
    endpoint TEXT NOT NULL,
    status INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    error_code TEXT
);
CREATE INDEX idx_oplog_ts ON operation_log(timestamp);

-- Pairing tokens (short-lived)
CREATE TABLE pairing_tokens (
    token TEXT PRIMARY KEY,
    numeric_code TEXT,
    initiated_by_principal TEXT NOT NULL,
    initiated_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    completed_at TEXT,
    completed_by_device TEXT,
    fif_integrity_commitment BLOB
);
CREATE INDEX idx_pairing_expires ON pairing_tokens(expires_at);
```

### Blob storage

- Path: `blobs/<aa>/<bb>/<event_id>.bin` where `aa` and `bb` are the first 2+2 hex of SHA-256(event_id || namespace)
- File contents: raw ciphertext bytes (XChaCha20-Poly1305 output)
- Permissions: 0640 on Linux/macOS
- Operator backup target: this directory + the SQLite files

---

## Protocol endpoint implementation

The Hub implements every endpoint in `fabric-spec-001-v1.0.md`. Implementation requirements:

### Verification flow (every protected request)

```
1. Parse macaroon from X-Fabric-Grant
2. Verify macaroon signature chain (Hub validates issuer public key from principals table)
3. Check Grant not in revocation list (query discharges/ cache; fetch upstream if stale)
4. Verify caveats in order:
   - time-before / time-after vs server clock
   - principal-type matches the verified macaroon's principal
   - agent-id / device-id match
   - operation in scope intersection
   - namespace matches
   - rate: increment counter for {grant_id, window}; check against caveat limit
   - max-amount: validate from request payload
   - only-domain: validate Bridge target domain
   - requires-human-approval: queue operation, return 202 with pending_id
   - nondelegatable: refuse if Grant minting is the target operation
   - idempotency-required: refuse if X-Fabric-Idempotency-Key missing
   - discharge-from: verify discharge macaroon presence and freshness
5. Process operation
6. Log to operation_log
7. Return response with freshness object
```

### Idempotency flow (every state-changing request)

```
1. Extract X-Fabric-Idempotency-Key
2. Compute payload hash (SHA-256 of request body)
3. Look up (key, grant_id) in idempotency table
   - If found and payload_hash matches: return stored response (HTTP 200)
   - If found and payload_hash differs: return idempotency_conflict (HTTP 409)
   - If not found: continue to step 4
4. Acquire row-level lock on (key, grant_id) via transaction
5. Process operation
6. Within same transaction: insert idempotency row with response
7. Commit
```

### Causal ordering preservation

For Vault and History:
- Reads return events in `sequence_number` order within a stream
- Subscribe streams emit events as they're appended, with `sequence_number` included
- Cross-stream causal ordering is the consumer's responsibility (via `causal_dependencies` field)

### History append (with hash chain)

```
1. Fetch stream's current head_event_hash (lock the stream row)
2. Verify request's previous_event_hash matches
   - If mismatch: return conflict (HTTP 409)
3. Compute new event_hash = SHA-256(previous_event_hash || event_id || kind || payload_metadata || causal_dependencies)
4. Insert event with event_hash and previous_event_hash
5. Update streams.head_event_id and head_event_hash
6. Commit
7. Return event_id, event_hash, sequence_number
```

### Conflict detection in Vault

For Vault streams (no hash chain), the Hub detects "concurrent" writes by looking at vector clocks:
- Append request includes vector_clock
- Hub checks: are there events in this stream with vector_clock values that are NOT ancestors of this clock AND not descendants?
- If yes: this is a concurrent write. Hub MAY accept both (append-only, both events stored) and emit a conflict event via Subscribe streams.

The Hub does NOT decide how to merge concurrent writes. It surfaces them.

### Discharge macaroon issuance

For Grants carrying `discharge-from <verifier-url>` where `<verifier-url>` is this Hub:
- Hub maintains a revocation History stream (e.g., `_revocations`)
- On Grant verification, Hub checks: has a revocation event been appended for this grant_id?
  - If no: issue a discharge macaroon with TTL = discharge.default_ttl_seconds
  - If yes: refuse to issue discharge (`grant_revoked`)
- Discharge macaroons are HMAC'd with the Hub's discharge key

Discharge endpoint: `POST /fabric/v1/grant/discharge` (called by clients before making operation requests)

---

## Sync between Hubs

When multiple Hubs are configured (Phase 2 / multi-anchor scenarios — for v1.0 max one peer):

### Pull cycle

```
Every sync.poll_interval_seconds:
  For each peer in peers table:
    1. Determine sync cursor (last event_id we have from this peer per stream)
    2. Call peer's GET /fabric/v1/sync/pull
    3. For each event returned:
       - Verify peer's signature on the event
       - Check namespace authorization (peer's Grant covers this stream)
       - Insert event into local events table (deduped by event_id)
       - Update stream head
    4. Update peers.last_sync_at
```

### Push cycle

Mirror of pull: if peer's `since` cursor is behind local head, push events.

### Failure handling

- Peer unreachable: log, mark peer.degraded, retry next cycle
- Peer signature invalid: log error, alert via Health endpoint
- Network partition longer than peer_timeout: events queue locally and replay on reconnect

---

## Pairing flow implementation

### Initiate

```
1. Verify Grant has identity:pair scope
2. Generate pairing_token (ULID), numeric_code (6-digit)
3. Compute fif_integrity_commitment from FIF metadata in initiating principal's identity
4. Store pairing_tokens row with expires_at = now + 10 min
5. Construct QR payload, magic_link
6. Return artifacts
```

### Complete

```
1. Receive pairing_token + new_device_public_key + new_device_name
2. Look up token; refuse if expired or already completed
3. Generate device_id (ULID)
4. Insert into principals as 'device' type, parent = the initiating principal
5. Mint an initial Grant for the new device (scope: identity:enroll, time-limited)
6. Mark pairing token completed
7. Return device_id, initial_grant, transport_configs (Hub URL)
```

The Hub does NOT store the new device's FIF; the device receives FIF out-of-band (typically the user re-enters passphrase on the new device against an FIF stored in their cloud backup, or transferred via AirDrop / similar).

---

## Operational concerns

### Process management

#### systemd (Linux)

`/etc/systemd/system/nakli-hub.service`:
```ini
[Unit]
Description=NakliTechie Fabric Hub
After=network.target

[Service]
Type=simple
User=nakli
Group=nakli
ExecStart=/usr/local/bin/nakli-hub serve --config /etc/nakli-hub/config.toml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/nakli-hub

[Install]
WantedBy=multi-user.target
```

#### launchd (macOS)

`~/Library/LaunchAgents/com.naklitechie.hub.plist`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.naklitechie.hub</string>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/nakli-hub</string>
      <string>serve</string>
      <string>--config</string>
      <string>$HOME/Library/Application Support/nakli-hub/config.toml</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardErrorPath</key><string>$HOME/Library/Logs/nakli-hub.log</string>
    <key>StandardOutPath</key><string>$HOME/Library/Logs/nakli-hub.log</string>
  </dict>
</plist>
```

### Logging

- Structured JSON to stdout (or file via launchd/systemd redirection)
- Levels: silent, error, warn, info, debug
- Default: info
- Operations logged with: timestamp, request_id, grant_id (last 8 chars), principal, endpoint, status, duration_ms, error_code
- Never log payload contents (they're encrypted anyway, but extra caution)
- Never log macaroon raw bytes

### Health endpoint

`GET /fabric/v1/health` returns:
```json
{
  "ok": true,
  "data": {
    "transport_id": "<hub-id>",
    "version": "naklimesh/1.0",
    "binary_version": "1.0.0",
    "uptime_seconds": 12345,
    "degraded": false,
    "degraded_reasons": [],
    "peer_health": [
      {
        "peer_id": "<peer-id>",
        "type": "hub",
        "reachable": true,
        "freshness_ms": 5000,
        "last_sync_at": "<rfc3339>"
      }
    ],
    "queue_depth": 0,
    "blob_count": 12345,
    "blob_total_bytes": 123456789,
    "event_count": 50000,
    "principals_count": { "human": 1, "agent": 3, "device": 4 }
  }
}
```

### Backup and restore

`nakli-hub backup --output <path>`:
- Performs SQLite `.backup` to a fresh file
- Tars SQLite backup + blobs/ + hub-identity.json + config.toml
- Operator stores wherever they want (encrypted backup recommended)

`nakli-hub restore --input <path>`:
- Refuses to run if data_dir is non-empty (use --force)
- Untars to data_dir
- Verifies SQLite integrity
- Verifies a sample of blob hashes match

Backup IS the disaster recovery. Loss of all backups + loss of hub-identity = permanent loss (cannot be re-paired because device principals identify against this Hub's identity).

### Upgrade

```
nakli-hub upgrade
```

1. Fetches latest version from `https://naklitechie.com/nakli-hub/latest.json`
2. Verifies GPG signature
3. Downloads binary
4. Verifies binary's SHA-256
5. Stops service (systemd/launchd)
6. Replaces binary atomically
7. Runs migrations if needed
8. Restarts service
9. Verifies health endpoint returns OK

Migrations are versioned in SQLite via `PRAGMA user_version`. Each version has a forward-only migration script embedded in the binary.

### Metrics

The Hub exposes Prometheus-compatible metrics at `/metrics` (separate port, default 7843, bind 127.0.0.1):
- `nakli_hub_requests_total{endpoint, status}`
- `nakli_hub_request_duration_seconds{endpoint}`
- `nakli_hub_events_total{namespace, stream_type}`
- `nakli_hub_blob_bytes`
- `nakli_hub_idempotency_keys_count`
- `nakli_hub_peer_freshness_seconds{peer_id}`
- `nakli_hub_queue_depth`

Metrics are local-only; Hub does not push anywhere. Operators can scrape from a local Prometheus or skip metrics entirely.

---

## Security posture

### What the Hub holds in clear
- Hub's own keypair (`hub-identity.json` — sensitive!)
- SQLite metadata: principal IDs, public keys, stream names, event IDs, vector clocks, timestamps, grant scopes
- Idempotency response bodies (which contain event IDs but not payloads)
- Operation log

### What the Hub holds encrypted
- Event payloads (in `blobs/`)
- All user data
- Bridge credentials (in transit only; never persisted by Hub)

### What the Hub never sees
- User passphrases (used only client-side for FIF)
- Per-namespace encryption keys (derived client-side from root)
- Bridge credential plaintext (decrypted client-side, used in transit to external service)
- Event payload plaintext

### Defense-in-depth measures
- File permissions: 0640 for all files in data_dir
- No remote management endpoints (no shell, no remote command execution)
- Bind to localhost by default; operator opens to network deliberately via reverse proxy or mesh
- No telemetry, no phone-home, no auto-update (operator runs `upgrade` manually)
- Rate-limit unauthenticated endpoints (`/health`, `/discover`, `/identity/pair/complete`)
- Macaroon verification is the ONLY auth; no fallback to other mechanisms

### Threat model

In-scope:
- Network adversary intercepts traffic — defeated by per-payload encryption
- Compromised agent attempts privilege escalation — defeated by macaroon attenuation enforcement
- Replay attacks — defeated by macaroon expiry and idempotency keys
- Forged macaroons — defeated by signature verification
- Stolen FIF without passphrase — defeated by AEAD envelope
- DoS via large requests — mitigated by max_event_size_bytes and rate limits

Out-of-scope:
- Root access to the Hub machine (operator's responsibility; encrypted disk recommended)
- Side-channel attacks on the Hub's cryptography (use standard library, no custom crypto)
- Quantum adversaries (Ed25519 is post-quantum-vulnerable; v2.0 may rotate to PQ algorithms)

### Audit logging

Operation log (in SQLite) records every request with timing and outcome. Operator can query directly:
```bash
sqlite3 fabric.db "SELECT timestamp, endpoint, status, principal, error_code FROM operation_log WHERE timestamp > datetime('now', '-1 hour')"
```

For long-term audit, History primitive is the canonical store (events the operator wants tracked are written to History streams; operation log is for debugging).

---

## Performance targets (v1.0)

- Append latency (single event, no contention): < 10 ms p99
- Read latency (100 events from a stream): < 50 ms p99
- Subscribe stream: < 100 ms event delivery from append
- Concurrent connections: 100 (single Hub instance)
- Storage: tested up to 10 million events / 100 GB blobs

These are not promises; they're rough targets for the reference implementation. Operators may scale below or above.

---

## Conformance

`nakli-hub` MUST pass all 32 tests in `fabric-spec-001-v1.0.md` conformance suite. The test suite runs via:
```bash
nakli-hub conformance --target http://localhost:7842
```

Test failures block release. Conformance is verified per commit in CI.

---

## Out of scope for v1.0

- Built-in HTTPS termination (use reverse proxy)
- Web admin UI
- Multi-node Hub clustering
- Storage backends other than SQLite + filesystem
- Stream archival / cold storage
- Per-event TTL (events are persistent until namespace deletion)
- Namespace deletion (deferred to v1.x)
- Quotas per principal

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md`
- Go SDK spec: `fabric-sdk-go-spec-001-v1.0.md`
- Vision doc: `private-mesh-vision-001-v0.7.md`
- Decisions: D5 (transport plurality), D9 (NetBird wrap), D10 (curl|bash installer)
