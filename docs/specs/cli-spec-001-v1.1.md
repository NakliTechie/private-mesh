# nakli-cli Specification

**Document:** `cli-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `cli-spec-001-v1.0.md` — adds explicit framework choice (Cobra) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md`, `fabric-sdk-go-spec-001-v1.0.md`, `hub-spec-001-v1.0.md`
**Audience:** Implementers of `nakli-cli`; operators using it.

`nakli-cli` is the reference command-line consumer of the Fabric Protocol. It is the operator surface: pair devices, mint Grants, provision agents, inspect queue, run conformance, manage transports, back up. It is also the human-readable view of the operations surface that agents talk to directly.

The CLI is built on `fabric-sdk-go`. It does no direct protocol speaking; everything goes through the SDK. This is the load-bearing point — the CLI is a thin shell. The SDK is the substance.

**Per D-Consumers, the CLI is first-class.** Not an afterthought; not deferred. Phase 1 includes the CLI alongside the Hub and SDKs.

---

## Scope

This document specifies:
- Command structure and conventions
- Every v1.0 command with its flags and output format
- Output formats (human-readable default; `--json` for scripting)
- Configuration file
- Exit codes
- Conformance with the protocol
- Distribution

Out of scope:
- Interactive TUI features (some commands are interactive at prompts; no full TUI in v1.0)
- Shell completions (provided as auto-generated scripts; not specified here)
- Per-command implementation details

---

## Dependencies

### Required

- **`github.com/spf13/cobra`** — the canonical Go CLI framework. The spec's command structure (e.g. `nakli vault append`, `nakli identity pair init`) maps directly to Cobra commands and subcommands.
- **`fabric-sdk-go`** — the CLI is a heavy SDK consumer.
- **`github.com/oklog/ulid/v2`** — ULID generation (via SDK).

### Recommended

- **`github.com/spf13/viper`** — config file + env var binding. Cobra and Viper compose naturally; standard pairing.
- **`github.com/manifoldco/promptui`** OR **`github.com/AlecAivazis/survey/v2`** — interactive prompts (e.g. passphrase entry, pairing confirmation). Either is fine; whichever the agent prefers.
- **`github.com/charmbracelet/lipgloss`** — terminal styling for the `--pretty` output mode. Used sparingly; the CLI is plain by default.

### Forbidden

- urfave/cli — Cobra is the spec choice for consistency with the broader Go ecosystem (kubectl, gh, etc.). Don't deliberate.
- Custom argument parsing. Use Cobra.

---

## Distribution

- Binary name: `nakli-cli`
- Single static Go binary (cgo disabled)
- Platforms: linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64
- Size: ~30-50 MB
- Installation:
  - `curl|bash` installer (per D10): `curl -fsSL https://naklitechie.com/install/cli | bash`
  - Homebrew (later): `brew install naklitechie/tap/nakli-cli`
  - Direct download with GPG sig
- Version: matches Fabric SDK Go version (CLI is a thin wrapper)

---

## Conventions

### Output

- Default: human-readable, tabular where appropriate, colored when stdout is a TTY
- `--json`: machine-readable JSON output
- `--quiet`: suppresses non-essential output (still prints errors)
- `--verbose` or `-v`: extra detail (timing, request IDs, transport selection)

### Configuration

CLI reads config from (in order):
1. Command-line flags
2. Environment variables (`NAKLI_*`)
3. Config file at `--config <path>` or `$NAKLI_CONFIG` or `~/.config/nakli-cli/config.toml`

```toml
# ~/.config/nakli-cli/config.toml

[cli]
default_fif = "~/.config/nakli-cli/identity.fif"
default_transport = "hub"        # tag matching a transport entry below
queue_db = "~/.local/state/nakli-cli/queue.db"
log_level = "info"

[[transport]]
tag = "hub"
type = "hub"
url = "https://hub.bhai.local"
preference = 1

[[transport]]
tag = "cf-worker"
type = "cf-worker"
url = "https://fabric.bhai.workers.dev"
preference = 2

[[transport]]
tag = "local"
type = "local-network"
preference = 0    # most preferred when available
```

Passphrase is never in config. CLI prompts interactively or accepts via stdin (`--passphrase-stdin`).

### Exit codes

- `0` — success
- `1` — general error
- `2` — usage error (bad flags, missing args)
- `3` — authentication failure (FIF unlock, wrong passphrase)
- `4` — Grant or scope error
- `5` — transport unavailable (after all retries)
- `6` — conflict (idempotency, concurrent write, hash chain)
- `7` — operation queued (returned with `--wait=false` when async)

### Common flags (every command)

```
--config PATH              Config file path
--fif PATH                 FIF file path (overrides default_fif)
--passphrase-stdin         Read passphrase from stdin (non-interactive)
--transport TAG            Transport tag from config (overrides default_transport)
--json                     JSON output
--quiet                    Minimal output
--verbose, -v              Verbose output
--no-color                 Disable ANSI colors
--timeout DURATION         Per-request timeout (default 30s)
--wait BOOL                Block on async operations (default true)
```

---

## Command structure

```
nakli-cli <command-group> <command> [args] [flags]
```

Command groups:
- `identity` — FIF management, pairing, agents
- `grant` — Grant minting, inspection, revocation
- `vault` — Vault primitive (manual operations)
- `history` — History primitive
- `bridge` — Bridge primitive
- `llm` — LLM primitive
- `transport` — transport management
- `queue` — operation queue inspection
- `status` — fabric health and freshness
- `conformance` — run conformance suite
- (root-level) — `init`, `version`, `help`

---

## Identity commands

### `nakli-cli identity init`
Generate a new FIF.

```bash
nakli-cli identity init --display-name "Bhai" --output ~/.config/nakli-cli/identity.fif
```

Flags:
- `--display-name STR` (required)
- `--output PATH` (required)
- `--envelope TYPE` — default `passphrase-only` (only option in v1.0)

Output (human):
```
Created identity:
  Principal ID:   01HMXK...
  Public key:     ZGV2ZWxv...
  Display name:   Bhai
  FIF written to: /home/bhai/.config/nakli-cli/identity.fif

Next steps:
  - Set passphrase via your password manager
  - nakli-cli transport add ... to register your transports
  - nakli-cli identity pair to enroll additional devices
```

### `nakli-cli identity show`
Display current identity.

```bash
nakli-cli identity show
```

Output:
```
Principal:       01HMXK...
Type:            human
Display name:    Bhai
Devices:         3 (M4-Pro, M4-Max-Studio, iPad)
Agents:          2 active, 1 retired
Transports:      3 configured
Grants held:     14 (10 self, 4 delegated to agents)
```

### `nakli-cli identity pair`
Initiate device pairing.

```bash
nakli-cli identity pair --method qr
```

Flags:
- `--method qr|code|link` (default `code`)

Output for `qr`:
```
Pairing initiated.
  Token:        01HMXY...
  Expires:      2026-05-15 18:40:00 UTC

QR Code:
  ████ ▄▄▄▄ ████  ▄▄▄▄
  ████ ████ ████  ████
  ...

Or numeric code:  847-291
Or link:          https://hub.bhai.local/fabric/v1/identity/pair/complete?token=01HMXY...

Waiting for completion (Ctrl-C to cancel)...
```

The command stays alive until the pairing completes or expires, then prints a confirmation.

### `nakli-cli identity pair complete`
On the new device — accept a pairing token.

```bash
nakli-cli identity pair complete --token 01HMXY... --device-name "MacBook Pro" --fif-output ./identity.fif
```

Asks for the existing-device's FIF passphrase out-of-band so the new device can decrypt the FIF (this is a separate UX decision documented in vision).

### `nakli-cli identity agents list`
```bash
nakli-cli identity agents list
```

```
AGENT ID            NAME                       VENDOR     PROVISIONED          STATUS
01HMXA...           claude-code-tax-prep       anthropic  2026-05-10 14:00 UTC active
01HMXB...           backup-runner              local      2026-05-12 09:00 UTC active
01HMXC...           grocery-bot                openai     2026-04-22 11:00 UTC retired
```

### `nakli-cli identity agents provision`
```bash
nakli-cli identity agents provision \
    --name "claude-code-bahi-entries" \
    --vendor anthropic \
    --expires-in 30d
```

The CLI generates a keypair locally (or accepts an externally-generated public key via `--public-key`), provisions the agent identity, and outputs:

```
Provisioned agent:
  Agent ID:    01HMXD...
  Name:        claude-code-bahi-entries
  Vendor:      anthropic
  Expires:     2026-06-15 18:30:00 UTC
  Public key:  ZGV2...
  Private key (write this to the agent's FIF; will not be shown again):
    LS0tLS1CRUdJTiBQUklWQVRF...

Next: mint a Grant for this agent with `nakli-cli grant mint --recipient 01HMXD...`
```

The private key is only printed once. The CLI may optionally write directly to a path:
```bash
... --output-private-key /run/agent/agent-key.json
```

### `nakli-cli identity agents retire`
```bash
nakli-cli identity agents retire 01HMXA... --reason "Tax season ended"
```

Writes a retirement event to History; all Grants minted by/for this agent become invalid.

---

## Grant commands

### `nakli-cli grant mint`
Mint a Grant.

```bash
nakli-cli grant mint \
    --recipient 01HMXD... \
    --primitive vault \
    --namespace bahi \
    --operations read \
    --expires-in 30d \
    --rate "1000/hour" \
    --output bahi-read.macaroon
```

Flags:
- `--recipient PRINCIPAL_ID` (required)
- `--primitive STR` — vault|history|bridge|llm|sync|identity|grant
- `--namespace STR` — namespace or `*`
- `--operations LIST` — comma-separated
- `--expires-in DURATION` — e.g., `30d`, `12h`, `90s`
- `--expires-at RFC3339` — alternative to `--expires-in`
- `--rate SPEC` — e.g., `1000/hour`, `10/minute`
- `--max-amount SPEC` — e.g., `1000USD` for Bridge calls
- `--only-domain LIST` — comma-separated, for Bridge calls
- `--requires-human-approval` — flag for Bridge calls
- `--nondelegatable` — flag
- `--parent-grant PATH` — for delegation
- `--output PATH` — write macaroon to file (default: stdout)

Output (human):
```
Minted grant 01HMXE...
  Recipient:     01HMXD...
  Scope:         vault:bahi (read)
  Expires:       2026-06-15 18:30:00 UTC (30d)
  Caveats:       rate=1000/hour
  Macaroon:      bahi-read.macaroon (286 bytes)
```

### `nakli-cli grant inspect`
Inspect a Grant's structure.

```bash
nakli-cli grant inspect bahi-read.macaroon
```

```
Grant 01HMXE...
  Issued by:      01HMXK... (Bhai)
  Recipient:      01HMXD... (claude-code-bahi-entries)
  Parent grant:   <root>
  Scope:
    Primitive:    vault
    Namespace:    bahi
    Operations:   read
  Caveats:
    time < 2026-06-15T18:30:00Z
    principal-type in [agent]
    agent-id == 01HMXD...
    rate <= 1000 per hour
    idempotency-required
  Issued at:      2026-05-15 18:30:00 UTC
  Verification:   OK
```

### `nakli-cli grant verify`
Verify a Grant for a hypothetical operation.

```bash
nakli-cli grant verify bahi-read.macaroon --primitive vault --namespace bahi --operation read
```

```
Would succeed: yes
Caveats checked:
  time < 2026-06-15: OK (29d, 23h, 58m remaining)
  principal-type in [agent]: OK
  rate <= 1000/hour: OK (0/1000 used in current window)
```

### `nakli-cli grant revoke`
```bash
nakli-cli grant revoke 01HMXE... --reason "Agent compromised"
```

Writes a revocation event; subsequent uses of this Grant fail with `grant_revoked`.

### `nakli-cli grant list`
```bash
nakli-cli grant list --filter recipient=01HMXD...
```

---

## Vault commands

### `nakli-cli vault append`
```bash
echo '{"item": "milk", "qty": 2}' | nakli-cli vault append \
    --namespace list \
    --stream-id 01HMXL... \
    --kind list:item-added \
    --grant bahi-write.macaroon
```

Reads payload from stdin (or `--payload-file`). Output:
```
Appended event 01HMXM...
  Stream:        list:01HMXL...
  Sequence:      42
  Idempotency:   01HMXN...
```

### `nakli-cli vault read`
```bash
nakli-cli vault read --namespace list --stream-id 01HMXL... --since 01HMXM... --limit 100
```

Outputs events as JSON lines by default (Vault events have structured payloads).

### `nakli-cli vault subscribe`
```bash
nakli-cli vault subscribe --namespace list --stream-id 01HMXL...
```

Streams events to stdout as they arrive. Ctrl-C to exit.

### `nakli-cli vault streams`
```bash
nakli-cli vault streams --namespace list
```

Lists all streams in a namespace.

---

## History commands

### `nakli-cli history append`
Same shape as `vault append`, but writes to a History stream (hash-chained).

### `nakli-cli history verify`
```bash
nakli-cli history verify --stream-id audit-log
```

```
Stream: audit-log
  Length:        1,247 events
  Head hash:     8c3e...
  Verification:  OK (chain intact)
```

### `nakli-cli history read`
Same shape as `vault read`.

---

## Bridge commands

### `nakli-cli bridge call`
```bash
nakli-cli bridge call \
    --adapter courtlistener \
    --operation search \
    --params '{"q": "fourth amendment", "limit": 10}' \
    --grant bridge-courtlistener.macaroon
```

Output:
```
Bridge call completed.
  Adapter:       courtlistener
  Operation:     search
  Duration:      234ms
  Cost:          $0.00
  Result:        (printed below as JSON)
```

If the Grant has `requires-human-approval`:
```
Bridge call queued (pending approval).
  Pending ID:    01HMXP...
  Adapter:       email
  Operation:     send

  An authorized human must approve via: nakli-cli bridge approve 01HMXP...
```

### `nakli-cli bridge approve`
```bash
nakli-cli bridge approve 01HMXP...
```

Shows the pending operation, prompts for confirmation, executes if approved.

### `nakli-cli bridge pending`
```bash
nakli-cli bridge pending
```

Lists pending operations.

### `nakli-cli bridge adapters`
Lists installed adapters and their capabilities.

---

## LLM commands

### `nakli-cli llm complete`
```bash
echo "Summarize this in one paragraph: ..." | nakli-cli llm complete \
    --grant llm-invoke.macaroon \
    --preferred-route auto
```

### `nakli-cli llm routes`
```bash
nakli-cli llm routes
```

```
ROUTE          STATUS     LATENCY   CAPABILITIES
local          available  12ms      32k context, no vision, function-calling
browser-local  unknown    -         (not initialized in CLI context)
remote-claude  available  450ms     200k context, vision, function-calling
```

---

## Transport commands

### `nakli-cli transport add`
```bash
nakli-cli transport add \
    --tag hub \
    --type hub \
    --url https://hub.bhai.local \
    --preference 1
```

### `nakli-cli transport list`
```bash
nakli-cli transport list
```

```
TAG          TYPE            URL                              PREF   STATUS
local        local-network   _nakli-fabric._tcp.local         0      2 peers
hub          hub             https://hub.bhai.local           1      OK
cf-worker    cf-worker       https://nakli.bhai.workers.dev   2      OK
```

### `nakli-cli transport remove`
```bash
nakli-cli transport remove cf-worker
```

### `nakli-cli transport ping`
```bash
nakli-cli transport ping hub
```

```
hub (https://hub.bhai.local)
  Version:     naklimesh/1.0
  Reachable:   yes (latency 4ms)
  Degraded:    no
  Queue:       0 pending
  Peer sync:   2 peers, max staleness 12s
```

---

## Queue commands

### `nakli-cli queue list`
```bash
nakli-cli queue list
```

```
OP ID          PRIMITIVE   ENDPOINT                  ATTEMPTS   NEXT ATTEMPT       LAST ERROR
01HMXQ...      vault       /fabric/v1/vault/append   3          in 32s             unavailable
01HMXR...      bridge      /fabric/v1/bridge/call    1          in 8s              unavailable
```

### `nakli-cli queue retry`
```bash
nakli-cli queue retry 01HMXQ...
```

### `nakli-cli queue cancel`
```bash
nakli-cli queue cancel 01HMXQ...
```

### `nakli-cli queue clear`
```bash
nakli-cli queue clear --older-than 7d
```

Bulk cancel matching operations.

---

## Status command

### `nakli-cli status`
```bash
nakli-cli status
```

```
Identity:
  Principal:     01HMXK... (Bhai)
  FIF unlocked:  yes
  Devices:       3 enrolled
  Agents:        2 active

Transports:
  local        OK (2 peers)        latency 4ms
  hub          OK                  latency 6ms
  cf-worker    OK                  latency 142ms

Freshness:
  Max staleness: 12s
  Peers synced:  2/2
  Status:        healthy

Queue:
  Pending:       0
  Failed perm:   0 (last 24h)

Conflicts:
  Open:          0
```

`--json` produces machine-readable form.

---

## Conformance command

### `nakli-cli conformance`
```bash
nakli-cli conformance --target https://hub.bhai.local
```

Runs the 32-test conformance suite from `fabric-spec-001-v1.0.md`. Output:

```
Running conformance suite: target https://hub.bhai.local
naklimesh/1.0 (32 tests)

[ 1/32] Wire format: reject malformed JSON              ... PASS (12ms)
[ 2/32] Wire format: reject unknown protocol version    ... PASS (8ms)
[ 3/32] Wire format: return CORS headers                ... PASS (4ms)
...
[32/32] Adversarial: reject delegation omitting caveats ... PASS (18ms)

Result: 32/32 passed
```

Failures include details:
```
[15/32] Grant: refuse delegation that would widen scope ... FAIL
  Expected: scope_denied
  Got: 200 OK (operation succeeded)
  Detail: Delegated Grant with broader scope was accepted; this is a critical security failure.
```

Flags:
- `--skip-tests LIST` — skip specific tests
- `--grant PATH` — Grant with conformance scope (typically `--scope=* --operations=*`)
- `--report PATH` — write detailed report to file
- `--continue-on-fail` — don't stop at first failure

---

## Backup / restore (CLI-level)

### `nakli-cli backup`
```bash
nakli-cli backup --output bhai-fabric-backup-2026-05-15.tar
```

Backs up:
- FIF (encrypted; user's passphrase still required to use it)
- Queue database
- Local config

Does NOT back up the Hub's state (use `nakli-hub backup` for that).

### `nakli-cli restore`
```bash
nakli-cli restore --input bhai-fabric-backup-2026-05-15.tar --to ~/.config/nakli-cli/
```

---

## Init command

### `nakli-cli init`
Interactive first-run setup.

```bash
nakli-cli init
```

```
Welcome to nakli-cli.

This wizard sets up a new Fabric identity and configures a transport.

Step 1: Identity
> Display name: Bhai
> Output path for FIF (default: ~/.config/nakli-cli/identity.fif):
> Passphrase: ****
> Confirm:    ****

Generated identity 01HMXK....

Step 2: Transport
Choose your primary transport:
  [1] Hub (you run a server)
  [2] Cloudflare Worker (zero-ops, deploy to your account)
  [3] Local Network only (advanced; LAN-only setup)
> Choice: 1

> Hub URL (default: https://localhost:7842):

Tested connection: OK.
Configured transport 'hub' with preference 1.

Step 3: Generate a self-Grant
A root self-Grant lets this CLI perform operations on your behalf.
Scope: * (everything)
Expires in: 1y

Generated grant 01HMXS....

Setup complete. Try:
  nakli-cli status
  nakli-cli vault append --help
```

---

## Auxiliary commands

### `nakli-cli generate-hub-identity`
Generates a Hub identity keypair (used during Hub deploy, not for personal identity).

```bash
nakli-cli generate-hub-identity > hub-identity.json
```

### `nakli-cli generate-ulid`
```bash
nakli-cli generate-ulid
```
Outputs a fresh ULID. Useful for scripting.

### `nakli-cli version`
```
nakli-cli 1.0.0
  fabric-sdk-go 1.0.0
  Protocol: naklimesh/1.0
  Build: 2026-05-15-abc1234
  Go: 1.22.3
```

### `nakli-cli completion`
Generates shell completion scripts.

```bash
nakli-cli completion bash > /etc/bash_completion.d/nakli-cli
nakli-cli completion zsh > "${fpath[1]}/_nakli-cli"
nakli-cli completion fish > ~/.config/fish/completions/nakli-cli.fish
```

---

## JSON output mode

With `--json`, every command emits a single JSON object on stdout:

```bash
$ nakli-cli vault append --json ... 
{
  "ok": true,
  "data": {
    "event_id": "01HMXM...",
    "sequence_number": 42,
    "idempotency_key": "01HMXN..."
  }
}

$ nakli-cli grant inspect bad.macaroon --json
{
  "ok": false,
  "error": {
    "code": "fif_format",
    "message": "Macaroon parse failed: invalid signature"
  }
}
```

Error responses still exit non-zero (per exit codes), so shell scripts can:
```bash
result=$(nakli-cli ... --json) || { echo "failed"; exit 1; }
event_id=$(echo "$result" | jq -r .data.event_id)
```

---

## Logging

Logs go to stderr; stdout is reserved for command output.

Levels (via `--log-level` or `NAKLI_LOG_LEVEL`):
- `silent` — no log output
- `error` — errors only
- `warn` — warnings and errors
- `info` — informational (default)
- `debug` — full trace including request IDs

---

## Agent compatibility

The CLI is the human-readable view of an interface agents speak directly. Specifically:
- Every CLI command has a corresponding JSON output mode
- Every CLI command can be invoked from a script with deterministic output structure
- Long-running interactive commands (e.g., `identity pair`) have non-interactive variants (e.g., `--non-interactive --token-output PATH`)
- Errors are structured with codes that match the protocol error codes

This makes the CLI scriptable from both human shell and agent runtime.

---

## Conformance with protocol

The CLI MUST:
- Generate ULID idempotency keys for all state-changing operations (via SDK)
- Honor `freshness` data on every response
- Use the Go SDK's queue for retry semantics
- Refuse to operate when the FIF is locked (`nakli-cli lock` to lock)
- Print accurate error codes that match the protocol

---

## Out of scope for v1.0

- Plugin / extension mechanism for custom commands
- Interactive shell mode (`nakli-cli shell`)
- Cross-platform service installation (use OS-native tooling)
- Built-in editor for payload composition (use $EDITOR or pipe stdin)

---

## References

- Go SDK spec: `fabric-sdk-go-spec-001-v1.0.md`
- Protocol spec: `fabric-spec-001-v1.0.md`
- Hub spec: `hub-spec-001-v1.0.md`
- Decisions: D-Consumers (CLI as first-class), D10 (curl|bash installer)
