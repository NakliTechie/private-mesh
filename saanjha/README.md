# saanjha

Shared-list consumer tool (working name; final name TBD at launch). Phase 1's flagship consumer of the Private Mesh: a single-HTML, zero-account, multi-user list that syncs across devices using fabric Vault streams.

**Status:** alpha — **M8 complete** (all 5 sessions). The deliverable is `saanjha.html` — a single self-contained file (~70 KB) that runs in two modes:

- **Demo mode** (default) — local mock data, no backend; the read-only-with-mock surface that anchors the session-1 smoke.
- **Fabric mode** — talks to a real Hub via [fabric-sdk-js](../fabric-sdk-js): FIF unlock + `vault.append`/`vault.read` + polling subscribe + freshness indicator + per-stream membership. Switches over via the "Set up sync" modal (production path) or `window.__GATE` (test injection).

## Sessions

| Session | What landed |
| --- | --- |
| 1 | HTML/CSS layout, `materializeList()`, mock event log, local CRUD, filter, inline edit, a11y basics |
| 2 | Store abstraction (`DemoStore` / `FabricStore`), FIF unlock flow, vault.read + append + polling, freshness indicator, banner |
| 3 | Multi-list switcher, fractional-indexing (inline, ~30 LOC), drag-to-reorder + move-up/down arrows, qty inline edit |
| 4 | Operator side-drawer (sync status / members / lists / conflicts / export / about), .naklilist export per spec |
| 5 | a11y audit (skip-link, ARIA-hidden subtree handling, label-association check), keyboard Escape closes overlays, sticky footer |

## Quick start

```sh
./smoke.sh                                  # single-file + size constraint
open saanjha.html                           # demo mode (mock data)
```

Cross-browser gates:

```sh
../scripts/saanjha-gate.sh                  # demo-mode (s1 + s3 + s4) — Chromium + Firefox + WebKit, 27 tests
../scripts/saanjha-fabric-gate.sh           # session 2 — spins up Hub + cli grant mint, 9 tests
PLAYWRIGHT_PROJECT=firefox ../scripts/saanjha-gate.sh
```

## Architecture

Behind one store interface (`read()`, `append(event)`, `subscribe(cb)`), two backends:

- `DemoStore` — purely in-memory event log; what session 1 shipped. Survives reloads only via the page being kept open.
- `FabricStore` — `fabric.vault.append` for writes, `fabric.vault.read({ sinceEventId })` polled every 1.5s for subscribe (the SDK's native `vault.subscribe` lands in M5.x SSE).

`materializeList()` is pure and shared — both stores feed it the same `{ kind, payload, event_id, appended_at, appended_by_principal }` shape.

A `StoreRegistry` keyed by list_key (`demo:<ulid>` or `fabric:<ulid>`) gives the multi-list switcher per-list cursors and queue depths without losing state when the user switches.

## Setup flow (Fabric mode)

1. Click the banner's **Set up sync** link (or the drawer's primary action under Sync status).
2. Modal asks for:
   - Hub URL (e.g. `http://127.0.0.1:7842` or your Cloudflare Worker)
   - Your FIF file (`.fif`)
   - Passphrase
   - Grant macaroon (base64; minted by the operator via `nakli-cli grant mint`)
3. SDK loads, FIF unlocks, identity goes into memory, grant is set, the first list reads from `/fabric/v1/vault/stream/saanjha/<stream_id>`.
4. Banner switches to `Connected to <hub> as <principal>`. Freshness indicator appears in the footer.

The setup modal is in-memory only; nothing is stored in `localStorage` or `sessionStorage`. Reloading the page re-prompts.

## .naklilist export

Spec §"File format". The drawer's Export panel produces:

```json
{
  "format": "naklilist/1.0",
  "exported_at": "...",
  "exported_by_principal": "...",
  "list": {
    "stream_id": "...",
    "namespace": "saanjha",
    "metadata": { "name": "Groceries", ... },
    "events": [ { "event_id": "...", "kind": "list:item-added", ... }, ... ]
  }
}
```

Use cases per the spec: backup, share with someone outside your Fabric, archive a completed list. Import lands at M8.x.

## Operator surface (hamburger → drawer)

| Panel | What |
| --- | --- |
| Sync status | Mode (Demo / Fabric Hub), freshness, queue depth, peers synced/missing |
| Members | Every distinct principal who's authored an event on this list, with event count and agent/human chip |
| Lists | All lists in the current registry |
| Conflicts | Open conflicts (rare; spec §"Conflict surface" — the resolver UI lands at M8.x) |
| Export | Download `.naklilist` |
| About | Saanjha version + whether the SDK is loaded |

## What's deliberately NOT here yet (M8.x / M9)

| Concern | Where |
| --- | --- |
| Native SDK `vault.subscribe` (SSE) — currently polled at 1.5s | M5.x |
| Conflict-resolution UI (review banner is wired; the resolver isn't) | M8.x |
| Recurring items / receipt-photo / aisle grouping / push notifications | v2 |
| Push-to-CDN deploy at `list.naklitechie.com` | M9 |
| .naklilist import | M8.x |
| Real fractional-indexing CDN dep (currently inline ~30 LOC) | optional polish |

## Test hooks

`window.__SAANJHA__` exposes:

```ts
{
  materializeList, fiBetween, version,
  getState(), getItems(), getEvents(), getFreshness(), getQueueSize(),
  openSetup(), closeSetup(), openDrawer(), closeDrawer(),
  exportList(), forceRender(),
  switchList(key), createList(name),
}
```

The Fabric gate (`scripts/saanjha-fabric-gate.sh`) seeds `window.__GATE = { hubUrl, grant, rootSeedHex, streamId, namespace, principalId }` before the page loads; saanjha detects this and bypasses the FIF flow via `fabric._useRootSeed`.

## Operational notes

- Deploy target: `list.naklitechie.com` (Cloudflare Pages) — M9.
- No backend other than the user's chosen Private Mesh transport (Hub, CF Worker, or Local Network).
- No analytics, no telemetry, no phone-home.

## Security notes

- FIF unlock state lives in memory only. No `localStorage` / `sessionStorage`.
- Tool-local settings will use IndexedDB (M8.x).
- All data on the wire and at rest in transport storage is encrypted client-side via fabric primitives.

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
