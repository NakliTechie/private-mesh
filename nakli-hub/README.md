# nakli-hub

Hub binary — the canonical Private Mesh transport. Runs on the user's anchor (a small always-on machine, typically self-hosted) and serves the fabric protocol over HTTP.

**Status:** alpha (M2 phase 2a — gate met: `nakli-hub serve` starts; `/fabric/v1/health` returns ok; manually-crafted Grant + Vault append/read works end-to-end. Phase 2b adds history, identity/pair, grant endpoints, sync, llm, bridge, backup/restore, service units.)

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

| Command | Status |
| --- | --- |
| `init`    | M2a — generates `hub-identity.json`, runs migrations, writes `config.json` |
| `serve`   | M2a — starts the HTTP server |
| `version` | M2a |
| `status`, `backup`, `restore`, `conformance`, `upgrade` | M2b/c |

## Packages

- `internal/config/` — JSON config + defaults (matches `hub-spec-001-v1.1.md` §Configuration)
- `internal/hubid/` — Ed25519 identity keypair + 32-byte macaroon HMAC root key, stored in `hub-identity.json`
- `internal/storage/` — SQLite open + WAL + migrations + blob writer; tables for principals, streams, events, idempotency, grants_known, peers, pending_bridge, operation_log, pairing_tokens
- `internal/server/` — HTTP mux (Go 1.22 patterns), slog logging, response envelopes, middleware (logging, CORS, macaroon auth, idempotency), handlers
- `cmd/nakli-hub/` — entrypoint

## Endpoints implemented (Phase 2a)

| Method + path | Auth | Notes |
| --- | --- | --- |
| `GET  /fabric/v1/health` | none | transport_id, version, uptime, principals_count, event_count |
| `GET  /fabric/v1/discover` | none | supported_primitives + caveat catalogue |
| `POST /fabric/v1/vault/append` | macaroon + idempotency | refuses `fabric.*` namespaces (forward-compat hook 6) |
| `GET  /fabric/v1/vault/stream/{namespace}/{stream_id}` | macaroon | `?since=<event-id>&limit=<n>` |
| `*    /fabric/v1/cluster/*` | none | HTTP 501 `not_implemented` (forward-compat hook 4) |

Phase 2b will add the remaining protocol surface (history, identity, grant, sync, llm, bridge) plus SSE for `/vault/subscribe`.

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

## Operational notes (planned for Phase 2b)

- systemd unit + macOS launchd plist
- `nakli-hub backup` / `restore` for snapshot
- `curl|bash` installer at M9 (per D10)
- No telemetry, no analytics, no auto-update

## Security notes

- The Hub holds the master macaroon HMAC root key (`MacaroonRootKey` in `hub-identity.json`) — guard this file like a private key (file mode 0600 by default)
- All event payloads land on disk as ciphertext (the Hub never sees plaintext)
- Macaroon signature verification is the only auth boundary; no fallback to origin / IP / shared secret
- CORS is permissive by protocol; that's not an auth mechanism — Grant verification is
- `fabric.*` Vault writes are refused from non-Hub principals (forward-compat hook 6)
- `/fabric/v1/cluster/*` returns 501 (forward-compat hook 4)
- Principal lookups accept `<ulid>@<fabric-id>` and ignore the suffix (forward-compat hook 5)

## Roadmap

- Phase 2b: history, identity (pair), grant, sync, llm, bridge endpoints; SSE for vault/subscribe; discharge macaroons; full caveat catalog
- Phase 2c: `backup` / `restore`, service units, `status`, `conformance` stub
- M3: full conformance suite (32 tests) in `fabric-sdk-go/conformance/`; `nakli-hub conformance` wires it
- M9: reproducible builds, GPG signing, `curl|bash` installer

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
