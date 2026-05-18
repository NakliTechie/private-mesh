# Crate — Vision and Roadmap v1.0

**Status:** Spec
**Repos:** `NakliTechie/crate` (browser surface), `NakliTechie/crate-agent` (native daemon)
**URL target:** `crate.naklitechie.com`
**Built on:** Sync + Vault + Identity + Grant + History primitives (fabric-spec-001, from `NakliTechie/private-mesh`)
**Cross-surface protocol:** `crate-pairing-protocol-v1.0.md` (browser↔daemon binding contract)

---

## What this is

A personal cloud folder. Your files live in a bucket you own (Cloudflare R2 by default), encrypted before they leave your browser. Open a tab, the folder is there. Run the optional daemon, the folder is in `~/crate/` on your machine too.

Dropbox-shaped utility, NakliTechie-shaped substrate.

## Two surfaces

|  | Browser | Native daemon |
|---|---|---|
| Where | `crate.naklitechie.com` | `crate-agent` (Go binary) |
| Repo | `NakliTechie/crate` | `NakliTechie/crate-agent` |
| Local storage | None (cloud only) | `~/crate/` |
| When syncing | Tab open | Daemon running |
| What it is | Single HTML page | Go binary, one per OS |
| Built on | `fabric-sdk-js` | `fabric-sdk-go` |
| Bucket credentials | Held in tab session | Never sees them (uses pairing token) |
| When in roadmap | v1.0 | v1.2+ |

Both surfaces are the same product, talking to the same bucket via the same Sync primitive. A user can run one, both, or neither.

## What this is not

- Not Dropbox. Dropbox holds the keys; Crate doesn't.
- Not a file viewer. Crate is the substrate. Folio renders PDFs, Slate renders images, Bahi renders ledgers. Crate hands them files.
- Not a new primitive. It's a consumer tool over Sync + Vault + Identity + Grant. The native daemon is a new transport sibling to `nakli-hub` / `nakli-cf-worker` / `nakli-local-bridge`.
- Not always-on by default. Tab closed = paused. That's the audience-correct shape.

## Why this shape

NakliTechie's audience opted out of always-on SaaS. Three things follow:

1. **BYOK is the default**, not the upgrade. The user owns the bucket, the keys, and the bill. NakliTechie operates a static site.
2. **Tab-scoped sync is correct**, not a limitation. People who want files to sync whether or not they're paying attention are asking for Dropbox, and Dropbox exists.
3. **Encryption is non-negotiable.** The bucket holds ciphertext. Bucket compromise leaks size and access pattern, not contents.

The interesting users:
- Want their data to be theirs (BYOK bucket → bill from R2 directly, not subscription to NakliTechie)
- Want to open a tab on any device and see their stuff
- Want NakliTechie tools (Folio, Slate, Bahi, Tijori, Mahalla) to read and write the same folder
- Will eventually want `rg`, `vim`, `obsidian-desktop`, and Claude Code to see those files too — that's when crate-agent earns its slot

## Licensing

Three licences across the system, each chosen for what it protects:

- **`NakliTechie/private-mesh` (primitives + SDKs):** **Apache-2.0**. Substrate wants adoption. Apache lowers friction for anyone integrating against fabric-spec-001, including commercial users. No copyleft.
- **`NakliTechie/crate` and `NakliTechie/crate-agent` (consumer apps):** **AGPL-3.0**. End-user products. If a company runs Crate as a hosted service for paying customers, they must publish their source under the same terms. Individuals and businesses can self-host freely. This is the OSI-approved copyleft option — protects against SaaS exploitation while staying genuinely open source.
- **Specs, docs, and methodology markdown in any repo:** **CC BY-SA 4.0**. Share-alike with attribution. Anyone can learn from the methodology and remix it; derivative docs must stay under the same licence.

Why this combination and not Apache-everywhere or AGPL-everywhere:

- Apache on primitives is correct — the whole point of a substrate is that other people build on it without licence drama.
- AGPL on consumer apps is correct — without it, a competitor with infrastructure could fork Crate, run it as a paid service, and never give anything back. AGPL doesn't stop forks; it just requires anyone running a modified version as a service to share their changes. This matches NakliTechie's audience principle: sovereignty for users, accountability for commercial operators.
- CC BY-SA on docs is correct — methodology should propagate. Someone learning from how Crate was specced is welcome to use the same shape for their own work, as long as their docs stay open too.

A coding agent contributing to these repos should add the appropriate licence header to new files and not introduce dependencies whose licences conflict (e.g. no GPL-2-only deps in an AGPL-3.0 repo; no proprietary deps anywhere).

## Locked decisions

### Cross-surface (apply to both browser and daemon)

1. **Single product, two surfaces.** Same Identity, same bucket, same manifest, same conflict semantics. The daemon doesn't replace the browser; it extends reach.
2. **Cloud is canonical.** Bucket is source of truth. Local materialisation (daemon) is a cache.
3. **BYOK bucket is the default.** Managed-bucket option exists at v1.1.
4. **AES-256-GCM client-side encryption.** Keys never leave the device (browser or daemon).
5. **Passphrase + recovery phrase model.** Passphrase derives the master key (PBKDF2 600k iterations). Recovery phrase (24 BIP-39 words) is an alternate path to the same key.
6. **Paths and filenames are encrypted too.** Bucket holds UUID-keyed blobs. Manifest holds (UUID → encrypted-path) entries. Manifest itself is encrypted.
7. **Manifest = append-only signed JSONL event log.** Maps directly onto History primitive.
8. **One Identity per folder.** Sharing happens via Grant primitive (v1.1).
9. **No accounts on NakliTechie's side, ever.** Identity is the user's keypair.
10. **Same wire protocol as fabric-spec-001.** Daemon does not get a private protocol; if Sync can't reach a daemon cleanly, fix Sync.

### Browser surface (v1.0)

11. **Single HTML file + ESM modules.** No build step. No framework. No bundler.
12. **No service worker in v1.0.** Tab-scoped only.
13. **BYOK credentials never persist by default.** Re-entered per session unless user explicitly opts in to sessionStorage ("remember in this tab"). Never localStorage. Never IndexedDB for creds.
14. **ESM API alongside human UI.** Other NakliTechie tools consume Crate as a filesystem.
15. **Onboarding is the product.** A guided wizard with deep-link buttons to the exact Cloudflare dashboard pages, validation between steps, copy-paste blocks for everything the user pastes back. Target: 3 minutes for first-timer.
16. **Browser holds bucket credentials.** No proxy in v1.0; browser talks to R2 directly via S3-compatible API.

### Native daemon (v1.2+, deferred)

17. **One Go binary per OS.** macOS + Linux at v1.2. Windows at v1.3. Mobile is a different product family.
18. **Plaintext on local disk by default.** That's the whole point — local tools read files.
19. **Config via TOML file.** `~/.config/nakli/crate-agent.toml`. CLI-only in v1.2.
20. **No File Provider / kext / virtual FS in v1.2.** Online-only placeholder files are v2.0 territory.
21. **Daemon does not hold bucket credentials.** This is a deliberate security property and shapes the architecture significantly. The daemon authenticates to a *transport* (a `nakli-cf-worker` or `nakli-hub` instance that does hold bucket credentials) using an Identity-scoped pairing token. The daemon binary on disk cannot leak Cloudflare keys it doesn't have.
22. **Pairing token model.** Tokens are issued by the browser surface during pairing, scoped to one Identity, time-limited at issuance (15 min to redeem), and grant the daemon long-lived access to the transport scoped to the user's Identity. Revocable from any device.
23. **Daemon talks to a transport, not directly to R2.** This means BYOK users running pure-Crate (no managed services) need a transport in their stack. Three options at v1.2:
    - Run `nakli-hub` on their NAS/home server (sovereign, takes setup)
    - Deploy `nakli-cf-worker` to their own Cloudflare account (cheap, requires Workers familiarity)
    - At v1.1+, use NakliTechie's managed transport (zero-setup, pay-per-call)

## Tool surface

### Human UI (browser)

Single-page HTML at `crate.naklitechie.com`:
- Onboarding wizard for first-time setup
- File tree (left), preview pane (right) after setup
- Drag-drop upload, click to download (decrypted in-browser)
- Rename, delete, move, mkdir
- "Open in [Folio | Slate | Bahi | …]" hand-off
- Device-pairing flow (QR + passphrase)
- Settings: bucket config, identity, encryption, device list, **daemon pairing**

### Agent face / ESM API

Same JS module exposed for other NakliTechie tools and agents:

```js
import { Crate } from "/crate/lib/crate.js";

const c = await Crate.open({
  bucket: { endpoint, region, accessKey, secretKey, bucketName },
  identity,
  passphrase,
});

await c.list("/notes");           // → [{path, size, mtime, mime, uuid}]
await c.read("/notes/foo.md");    // → Blob
await c.write("/notes/foo.md", blob);
await c.remove(path);
await c.move(from, to);
await c.mkdir(path);
await c.stat(path);
await c.history(path);
const unsub = c.onChange(handler);
```

This contract is what Folio, Slate, Bahi, Mahalla bind to. Once shipped, do not break.

### CLI (native daemon, v1.2+)

```
crate-agent start [--detach]
crate-agent stop
crate-agent status [--folder NAME] [--json]
crate-agent reconfigure
crate-agent doctor
crate-agent install-service / uninstall-service
crate-agent pair                              # accept pairing token from browser
crate-agent version
```

## Onboarding

The hardest non-technical problem. Three modes in the wizard:

### Mode 1 — BYOK guided wizard (v1.0 default)
- Deep-link buttons open the exact Cloudflare dashboard pages
- Pre-generated bucket name (unique by default, user can override)
- Copy-paste CORS JSON with screen-recording GIF showing where to paste
- Live validation: bucket-exists check, credentials test, CORS preflight
- Passphrase strength meter (entropy in bits, require ≥70 bits)
- 24-word recovery phrase with confirmation challenge
- Target: 3 minutes

### Mode 2 — Pair new device (v1.0)
- Existing device shows QR with endpoint + bucket + Identity proof
- New device scans (or paste pairing code) + enters passphrase
- Decrypt manifest, you're in
- Other devices get notified

### Mode 3 — Install agent (v1.2)
- Pick OS, download binary, verify SHA-256, install to PATH
- Generate pairing token from browser settings
- `crate-agent pair`, paste token, enter passphrase
- `crate-agent start` to test, `install-service` for auto-start
- Target: 5 minutes for command-line-comfortable users

### Mode 4 — Managed bucket (v1.1, not in v1.0)
- NakliTechie operates an R2 bucket on user's behalf, scoped to their Identity
- ~30 seconds onboarding, no Cloudflare interaction
- Billed via x402 or similar per-call

## Protecting the folder

Layered defence:

1. **AES-256-GCM at rest** — bucket compromise reveals nothing
2. **Strong passphrase** — refuse < 70 bits entropy, force diceware or equivalent
3. **24-word recovery phrase** — same key, different encoding; insurance against passphrase loss
4. **Bucket-scoped API token** — token leak doesn't expose other R2 buckets the user has
5. **Device-pairing notifications** — new device unlock alerts existing devices, with revoke action
6. **Per-session credential entry by default** — credentials in memory only unless user explicitly opts to remember in tab
7. **Path/filename encryption** — bucket inspection doesn't reveal folder structure
8. **Daemon never sees bucket credentials** — daemon binary on disk cannot leak cloud keys

## Audience fit

This is the substrate every other NakliTechie tool quietly needed:

- **Folio** — ebook library that follows you across devices
- **Slate** — image album not tied to one machine
- **Bahi** — ledger files not single-device
- **Mahalla** — Obsidian-vault-shaped store accessible when you're on a different laptop
- **Tijori** — sync without trusting a vendor
- **Bolo, Lunar, VoiceVault** — recordings/notes that persist

All of them currently solve this with "pick a folder via FSA" or "download to OPFS." Both break when the user changes devices. Crate is the answer. Once Crate ships, every other tool gets a `Crate.open()` call as an alternative to local FSA, and the portfolio becomes coherent across devices for the first time.

## Roadmap

### v1.0 — browser, BYOK, read/write a personal folder
- Onboarding wizard with deep links, validation, CORS check, passphrase strength, recovery phrase
- BYOK bucket config (R2, B2, S3, Hetzner — anything S3-compatible)
- AES-256-GCM client-side encryption, encrypted manifest, encrypted paths
- File tree view, upload, download, delete, rename, move, mkdir
- Device pairing (QR + passphrase)
- ESM API for other NakliTechie tools to consume
- Browser-only. Tab closed = inert.
- AGPL-3.0 licensed

### v1.1 — managed bucket + sharing
- NakliTechie-operated R2 bucket option (becomes default onboarding)
- x402 or equivalent per-call billing
- Grant primitive integration: share a subfolder with another Identity
- Read-only and read-write share modes
- Device notification panel (new device unlocks visible across devices)

### v1.2 — crate-agent (native daemon)
- Go binary, macOS + Linux
- Single watched folder, plaintext on disk, ciphertext in cloud
- CLI: start, stop, status, doctor
- launchd + systemd templates
- Bidirectional sync with browser tab
- Pairing-token authentication (no bucket creds on disk)
- New repo `NakliTechie/crate-agent`

### v1.3 — daemon polish
- Windows binary
- Multiple folders per machine
- Per-folder encryption-at-rest option
- Better conflict UX (currently: lose-event → `_conflicts/` folder)

### v1.4 — cross-tool consumption
- nakliOS integration: nakliOS picks a Crate as its root
- "Send to" menu for handing files between tools
- Per-folder encryption keys (share a folder without sharing master key)

### v2.0 — collaborative editing + virtual files
- Yjs over Sync for text files
- Real-time presence in folder view
- macOS File Provider extension (Swift, signed)
- Linux FUSE virtual filesystem
- Windows Cloud Files API
- "1 GB folder shown locally, 50 MB actually downloaded"

### v3.0 — mobile
- iOS File Provider extension (separate Swift codebase)
- Android Storage Access Framework

## What v2/v3 is not

- Not Dropbox-API-compatible
- Not peer-to-peer mesh (Sync routes through transports, not direct peering)
- Not a CDN or public sharing tool
- Not a "smart folder" with automations (that's Punto territory)

## Cost shape

### Per user

- BYOK v1.0: ~$0–$1/mo for typical user, paid to Cloudflare directly
- Managed v1.1: target $2/mo up to 10 GB, $5/mo to 100 GB, BYOK above
- Daemon: $0 — static binary download

### Per NakliTechie

- v1.0: $0 marginal cost per user. Static site on CF Pages.
- v1.1: bucket cost + relay compute. Priced from actual cost, not seat-based.
- v1.2+: $0 marginal cost added — daemon is a download.

## Wire-protocol audit (gate before v1.2)

Before crate-agent build starts, audit fabric-spec-001 for browser-isms:

1. Does the wire protocol assume WebSocket framing, or work over HTTP/HTTP2/QUIC/TCP?
2. Does Sync's session model assume tab-scoped lifetime (open/close events tied to UI presence)?
3. Is `fabric-sdk-go` callable from a long-running daemon, or only short-session clients?
4. Are auth tokens / Identity proofs scoped in a way that breaks for daemons?

Any "yes" surfaces a protocol revision. Fix at the protocol layer, not at the binary.

## Build sequence

1. **Onboarding wizard** (browser, no encryption yet) — proves the dashboard-deep-link approach works
2. **Encryption + manifest** — AES-GCM round-trip, signed JSONL, recovery phrase
3. **File operations + UI** — tree, upload, download, delete, rename, mkdir
4. **ESM API** — lock the agent-face contract
5. **Sync binding** — second device sees changes via fabric-sdk-js Sync
6. **Device pairing** — QR flow, passphrase unlock, device notifications
7. **Polish + ship** — error states, docs, deploy to crate.naklitechie.com

After v1.0 ships: managed bucket (v1.1) ships before daemon (v1.2). Bucket-management is more valuable to more users than a daemon.

## Open questions for build

None for the spec. Open during build:
- Whether bucket-name generation should be deterministic from Identity or random. Lean: random with override.
- Manifest format: pure signed JSONL vs JSONL-with-merkle. Lean: pure for v1.0, merkle when conflicts get real (v1.3).
- Whether to ship a CLI "crate" companion for power users on day one. Lean: no, defer to v1.2 when daemon ships.


---

*This document is licensed CC BY-SA 4.0.*
