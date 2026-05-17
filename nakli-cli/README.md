# nakli-cli

Reference CLI for the NakliTechie Private Mesh. The operator/developer surface — pair devices, mint Grants, append/read Vault streams, run conformance, manage transports.

**Status:** alpha — **M4 complete.** Gate-critical commands wired and end-to-end gate green (`./scripts/cli-gate.sh` reports `32/32 passing`). Deferred commands print clear pointers at the milestone where they land.

## Quick start

```sh
# 1. Build
go build ./cmd/nakli-cli

# 2. First-run setup (interactive)
./nakli-cli init

# 3. Mint a Grant + append to a Vault stream
./nakli-cli grant mint --recipient 01J... --primitive vault --namespace list \
    --operations read,write --output vault.macaroon
echo '{"item":"milk","qty":2}' | ./nakli-cli vault append \
    --namespace list --stream-id 01J... --kind list:item-added --grant vault.macaroon

# 4. Run conformance
./nakli-cli conformance --target http://127.0.0.1:7842 --hub-data-dir /path/to/hub-data
```

`nakli-cli version` prints the binary + protocol versions.

## Command surface

| Group | M4 status |
| --- | --- |
| `init` (wizard) | implemented |
| `identity init`, `identity show` | implemented |
| `identity pair`, `identity agents` | deferred → M4.x |
| `transport add` / `list` / `remove` / `ping` | implemented |
| `grant mint` / `inspect` | implemented |
| `grant verify` / `revoke` / `list` | deferred → M4.x |
| `vault append` / `read` / `streams` | implemented |
| `vault subscribe` | deferred → M4.x (SSE) |
| `history *` | deferred → M4.x |
| `bridge *` | deferred → M5.5 (adapter framework) |
| `llm *` | deferred → M5+ (SDK routing) |
| `queue *` | deferred → M5 (SDK queue package) |
| `status` | implemented |
| `conformance` | implemented (wraps `fabric-sdk-go/conformance`) |
| `backup` / `restore` | deferred → M4.x |
| `version`, `generate-ulid`, `generate-hub-identity` | implemented |
| `completion bash\|zsh\|fish\|powershell` | implemented (via cobra) |

## Conventions

- `--json` on every command emits `{ok, data}` or `{ok:false, error:{code,message}}` on stdout.
- `--quiet` suppresses non-essential output.
- `--verbose` adds extra detail (timing, request ids).
- `--passphrase-stdin` reads the FIF passphrase from stdin instead of prompting (for scripts).
- `--transport <tag>` overrides the config's `cli.default_transport`.
- `--config <path>` overrides `$NAKLI_CONFIG` and `~/.config/nakli-cli/config.toml`.

Exit codes follow the spec: `0` ok, `1` general error, `2` usage. Richer exit codes (3 auth, 4 grant/scope, 5 transport, 6 conflict, 7 queued) land at M4.x once the SDK's queue arrives.

## Configuration

```toml
# ~/.config/nakli-cli/config.toml
[cli]
default_fif = "/home/bhai/.config/nakli-cli/identity.fif"
default_transport = "hub"
queue_db = "/home/bhai/.local/state/nakli-cli/queue.db"
log_level = "info"

[[transport]]
tag = "hub"
type = "hub"
url = "http://127.0.0.1:7842"
preference = 1
# hub_data_dir is a CLI-side hint: when the CLI and Hub share a host, the
# CLI reads hub-identity.json from this path to mint Grants locally and to
# run the conformance suite without further flags.
hub_data_dir = "/var/lib/nakli-hub"
```

Passphrases are never written to the config — interactive prompt or `--passphrase-stdin`.

## Build / Test

```sh
go build ./...
go test ./...        # 6 tests passing as of M4
./smoke.sh           # build-all entrypoint
```

The end-to-end M4 gate lives at [`../scripts/cli-gate.sh`](../scripts/cli-gate.sh) — it builds both `nakli-hub` and `nakli-cli`, runs the Hub with an unreachable peer (for conformance test 26), and drives `init → grant mint → vault append → vault read → conformance` end-to-end.

## Packages

- `cmd/nakli-cli/` — entrypoint; sets `BinaryVersion`
- `internal/cmd/` — cobra command tree (one file per group)
- `internal/config/` — TOML config + env binding via viper
- `internal/fifio/` — FIF create / load / unlock + passphrase prompts (uses `golang.org/x/term`)
- `internal/httpc/` — Hub HTTP client; thin wrapper over net/http + the Hub's envelope shape
- `internal/output/` — human-vs-JSON output writer; each command sets data and prints human lines, the writer emits whichever the caller asked for

## Security notes

- The CLI never persists passphrases or unwrapped key material; the FIF is locked immediately after each command's serialize step.
- `grant mint`'s local-mint path requires read access to the Hub's `hub-identity.json` (which carries the macaroon root key). On multi-host setups the operator should pair an SDK-driven Grant flow instead — coming at M4.x.
- `--json` output never includes the FIF root key or the macaroon root key; only public principal ids, grant ids, and the macaroon wire bytes.

## Roadmap

- M4 (done): gate-critical commands (init, identity init/show, transport, grant mint/inspect, vault append/read/streams, conformance, status, utilities)
- M4.x: identity pair / agents, grant verify / revoke / list, vault subscribe (SSE), history, backup/restore, richer exit codes
- M5: JS SDK + the SDK's queue package (unblocks `queue` commands)
- M5.5: Bridge adapter framework + Bridge commands
- M9: `curl|bash` installer, signed releases

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
