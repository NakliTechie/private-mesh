# nakli-hub

Hub binary — the canonical Private Mesh transport. Runs on the user's anchor (a small always-on machine, typically self-hosted) and serves the fabric protocol over HTTP.

**Status:** alpha — **M3 complete.** All seven primitives implemented (vault, history, identity, grant, plus LLM/Bridge/Sync stubs honouring the forward-compat hooks); macaroon + idempotency middleware; **every Phase-1 caveat enforced** (rate / max-amount / only-domain / requires-human-approval / discharge-from now live alongside the M2-already-enforced caveats); `init` / `serve` / `backup` / `restore` / `status` / `conformance` / `version` subcommands; systemd unit + macOS launchd plist under `contrib/`. 24 in-process tests including the full 32-test conformance suite. Run `./scripts/test-conformance.sh` from the repo root for the gate.

## Quick start

```sh
# 1. Build
go build ./cmd/nakli-hub

# 2. Initialize a data directory (generates hub-identity.json + config.json + SQLite + blobs/)
./nakli-hub init --data-dir ./hub-data

# 3. Start the Hub
./nakli-hub serve --config ./hub-data/config.json

# 4. Smoke from another shell
curl http://127.0.0.1:7842/fabric/v1/health
curl http://127.0.0.1:7842/fabric/v1/discover
```

`nakli-hub version` prints the binary + protocol version.

## Subcommands

| Command | Notes |
| --- | --- |
| `init`        | generates `hub-identity.json`, runs migrations, writes `config.json` |
| `serve`       | starts the HTTP server |
| `backup`      | writes a gzip-tar archive (manifest + config + identity + VACUUM'd SQLite + blobs) |
| `restore`     | extracts an archive into a fresh data dir; runs SQLite `integrity_check` |
| `status`      | curls `/fabric/v1/health` against the local Hub and pretty-prints |
| `conformance` | `--target <url> --data-dir <dir>` — runs the 32-test suite from `fabric-sdk-go/conformance/` and exits non-zero on any failure |
| `version`     | prints binary + protocol version |

## Packages

- `internal/config/` — JSON config + defaults (matches `hub-spec-001-v1.1.md` §Configuration)
- `internal/hubid/` — Ed25519 identity keypair + 32-byte macaroon HMAC root key, stored in `hub-identity.json`
- `internal/storage/` — SQLite open + WAL + migrations + blob writer; tables for principals, streams, events, idempotency, grants_known, peers, pending_bridge, operation_log, pairing_tokens
- `internal/server/` — HTTP mux (Go 1.22 patterns), slog logging, response envelopes, middleware (logging, CORS, macaroon auth, idempotency), handlers
- `cmd/nakli-hub/` — entrypoint

## Endpoints implemented (Phase 2b)

### Public

| Method + path | Auth | Notes |
| --- | --- | --- |
| `GET  /fabric/v1/health` | none | transport_id, version, uptime, principals_count, event_count |
| `GET  /fabric/v1/discover` | none | supported_primitives + caveat catalogue |
| `POST /fabric/v1/identity/pair/complete` | pairing_token | enrolls a new device, returns enrollment Grant + transport configs |

### Authenticated (X-Fabric-Grant)

| Method + path | Idempotency | Notes |
| --- | --- | --- |
| `POST /fabric/v1/vault/append`              | required | refuses `fabric.*` namespaces (hook 6) |
| `GET  /fabric/v1/vault/stream/{ns}/{sid}`   | — | `?since=<event-id>&limit=<n>` |
| `GET  /fabric/v1/vault/streams/{ns}`        | — | summary of streams in namespace |
| `POST /fabric/v1/vault/subscribe`           | — | SSE; polling implementation |
| `POST /fabric/v1/history/append`            | required | hash chain validated; 409 on `previous_event_hash` mismatch |
| `GET  /fabric/v1/history/stream/{sid}`      | — | events with `previous_event_hash` + `event_hash` |
| `GET  /fabric/v1/history/verify/{sid}`      | — | walks chain end-to-end |
| `GET  /fabric/v1/identity/principal`        | — | returns Grant holder's principal info |
| `POST /fabric/v1/identity/pair/initiate`    | — | issues pairing token + QR + numeric code + magic link |
| `POST /fabric/v1/grant/mint`                | required | mints a Grant; enforces scope-narrowing + caveat-superset when `parent_grant_macaroon` is supplied |
| `POST /fabric/v1/grant/verify`              | — | hypothetical-operation check |
| `POST /fabric/v1/grant/revoke`              | required | writes revocation event to history |
| `POST /fabric/v1/grant/discharge`           | — | mints a discharge macaroon for a `discharge-from` caveat; refuses if Grant is revoked |
| `GET  /fabric/v1/llm/routes`                | — | empty in Phase 2b — SDK does remote-BYOK routing |
| `POST /fabric/v1/llm/complete`              | required | 501 — Hub does not proxy completions in v1.0 |
| `GET  /fabric/v1/bridge/adapters`           | — | empty in Phase 2b — adapter framework lands at M5.5 |
| `POST /fabric/v1/bridge/call`               | required | enforces `max-amount`/`only-domain`/`requires-human-approval`/idempotency caveats first; returns 202 + `pending_id` on human-approval; returns 501 for execution (M5.5) |
| `POST /fabric/v1/bridge/approve`            | — | marks `pending_bridge` row approved; requires `principal-type=human` |
| `GET  /fabric/v1/bridge/pending/{id}`       | — | returns pending bridge row by id (for the 202 polling flow) |
| `GET  /fabric/v1/sync/peers`                | — | empty in v1.0 single-anchor |
| `GET  /fabric/v1/sync/pull`                 | — | 501 — multi-anchor sync is Phase 2 |
| `POST /fabric/v1/sync/push`                 | — | 501 — multi-anchor sync is Phase 2 |
| `POST /fabric/v1/sync/conflict-ack`         | — | 501 — needs full conflict surfacing |

### Reserved

| Method + path | Notes |
| --- | --- |
| `* /fabric/v1/cluster/*` | HTTP 501 `not_implemented` (hook 4) |

## Caveat enforcement

M3 enforces every caveat in the spec's catalogue:

| Caveat | Enforced |
| --- | --- |
| `time < <rfc3339>`, `time > <rfc3339>` | server clock |
| `operation in [...]` | request operation |
| `namespace == <string>` | request namespace |
| `nondelegatable` | rejects on `/grant/mint` with a parent grant |
| `idempotency-required` | rejects when `X-Fabric-Idempotency-Key` is absent |
| `principal-type in [...]`, `agent-id == <ulid>`, `device-id == <ulid>` | cross-checked against `X-Fabric-Principal-Type`, `X-Fabric-Agent-Id`, `X-Fabric-Device-Id` request headers when the requester asserts an id; otherwise treated as Hub-trusted assertions |
| `rate <= N per <window>` | in-memory token bucket per `grant_id`; returns 429 `rate_limited` on exhaustion |
| `max-amount <= <int> <ccy>` | Bridge calls; rejects when `amount > N` or currency mismatch |
| `only-domain in [...]` | Bridge calls; rejects when call's `domain` is not in the list |
| `requires-human-approval` | Bridge calls; returns 202 + `pending_id`, row poll-able via `/bridge/pending/{id}` |
| `discharge-from <url>` | requires `X-Fabric-Discharge` carrying a discharge minted by `/grant/discharge`; the Hub refuses to mint discharges for revoked Grants |

## Build

```sh
go build ./...
```

Requires CGO (the SQLite driver `github.com/mattn/go-sqlite3` is CGO-based). On macOS / Linux this just works; cross-compilation needs a CGO toolchain.

## Test

```sh
go test ./...
```

The `server` package test exercises the M2 gate end-to-end: in-process HTTP server, hand-crafted macaroon Grant against the Hub's root key, Vault append + read, idempotent replay, idempotency conflict, scope refusal, unknown-key rejection, and the cluster/* reservation.

## Configuration

JSON file at the path passed via `--config`. Defaults match the spec:

| Key | Default | Notes |
| --- | --- | --- |
| `hub.listen` | `127.0.0.1:7842` | bind address; expose via reverse proxy / mesh |
| `hub.data_dir` | (required) | writable directory |
| `hub.log_level` | `info` | silent / error / warn / info / debug |
| `storage.sqlite_db` | `fabric.db` | under `data_dir` |
| `storage.blobs_dir` | `blobs` | under `data_dir` |
| `storage.max_event_size_bytes` | `1048576` | 1 MiB per spec |
| `storage.fsync_writes` | `true` | false acceptable only in tests |
| `idempotency.retention_seconds` | `86400` | ≥ 24h per spec (forward-compat hook 8) |
| `health.freshness_budget_seconds` | `86400` | |

A TOML pass may follow in Phase 2b once Bhai confirms config-format preference.

## Operational notes

- systemd unit + macOS launchd plist live under [`contrib/`](contrib/). See [`contrib/README.md`](contrib/README.md) for installation steps.
- `nakli-hub backup --config CFG --output PATH` snapshots the Hub. SQLite is captured via `VACUUM INTO` so the Hub may keep serving.
- `nakli-hub restore --input PATH --data-dir DIR` extracts an archive, validates SQLite, and refuses non-empty destinations without `--force`.
- `curl|bash` installer arrives at M9 (per D10). For now: `go install ./cmd/nakli-hub` or build manually.
- No telemetry, no analytics, no auto-update.

## Security notes

- The Hub holds the master macaroon HMAC root key (`MacaroonRootKey` in `hub-identity.json`) — guard this file like a private key (file mode 0600 by default)
- All event payloads land on disk as ciphertext (the Hub never sees plaintext)
- Macaroon signature verification is the only auth boundary; no fallback to origin / IP / shared secret
- CORS is permissive by protocol; that's not an auth mechanism — Grant verification is
- `fabric.*` Vault writes are refused from non-Hub principals (forward-compat hook 6)
- `/fabric/v1/cluster/*` returns 501 (forward-compat hook 4)
- Principal lookups accept `<ulid>@<fabric-id>` and ignore the suffix (forward-compat hook 5)

## Roadmap

- ✅ Phase 2c (M2): `backup` / `restore`, service units (systemd + launchd), `status`, conformance stub
- ✅ M3: 32-test conformance suite in `fabric-sdk-go/conformance/`; `nakli-hub conformance` wires it; all caveats enforced
- M4: reference CLI (`nakli-cli`) per `cli-spec-001-v1.1.md`
- M5: JS SDK (full primitives + IndexedDB queue + Web Locks leader election)
- M5.5: bridge adapter framework + 8 starter adapters
- M9: reproducible builds, GPG signing, `curl|bash` installer

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
