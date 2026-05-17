# saanjha

Shared-list consumer tool (working name; final name TBD at launch). Phase 1's flagship consumer of the Private Mesh: a single-HTML, zero-account, multi-user list that syncs across devices using fabric Vault streams.

**Status:** alpha — **M8 session 1 complete** (skeleton + read-only with mock data). The deliverable is `saanjha.html` — a single self-contained file (~27 KB) that materializes a list from an event log, supports add / check / uncheck / edit / delete / filter against local state, and renders presence chips for the principals (human + agent) who authored items. Fabric integration arrives in session 2.

## Sessions

| Session | Status | What lands |
| --- | --- | --- |
| 1 | done | HTML/CSS layout, materializeList(), mock event log, local CRUD, filter, inline edit, a11y basics |
| 2 | next | FIF unlock flow, fabric.vault.read/append/subscribe, freshness indicator |
| 3 | — | Multi-list switcher, fractional-indexing reorder, qty inline edit, conflict events |
| 4 | — | Operator surface (sync status, members, conflicts), .naklilist export |
| 5 | — | a11y audit, mobile UX polish, deploy |

## Quick start

```sh
./smoke.sh                                       # asserts saanjha.html is single-file ≤ 200 KB
open saanjha.html                                # or `xdg-open` on Linux
```

End-to-end cross-browser test:

```sh
../scripts/saanjha-gate.sh                       # Playwright on Chromium + Firefox + WebKit
PLAYWRIGHT_PROJECT=firefox ../scripts/saanjha-gate.sh
```

## What's in `saanjha.html`

Single file, no bundler, ~27 KB inlined. Pure JS (no framework) so the dep surface stays minimal — `fabric-sdk-js` joins in session 2 via CDN with SRI.

- **Layout** matches the spec's mobile-first mockup (`docs/specs/shared-list-spec-001-v1.0.md` §"Layout"): sticky topbar with hamburger / title / open-count / presence chips; sticky add-input row; flowing list of items; sticky footer with item count + filter chip.
- **Materialization** in `materializeList(events)` follows the spec verbatim (§"Materialization"). Handles all seven event kinds: `list:metadata`, `list:item-added`, `list:item-checked`, `list:item-unchecked`, `list:item-edited`, `list:item-reordered`, `list:item-deleted`. Unknown kinds are ignored (forward-compat).
- **Local state** is just `state.events[]` — every user action appends an event and re-renders. This is the exact shape session 2 will feed Fabric: `fabric.vault.append({ kind, payload })` swaps in cleanly.
- **Authors / presence** — each event carries `appended_by_principal`. The header shows distinct authors as initials chips; agents get a different shape (`∘` vs a letter). Per spec §"Showing who added an item".
- **Filter chip** — All / Open / Done. The button state mirrors `state.filter` via `aria-pressed`.
- **Inline edit** — tap or keyboard-activate (`Enter`/`Space`) the text to edit; `Enter` commits, `Escape` cancels.
- **Keyboard / a11y** — tab order works through the topbar, add input, each row's checkbox + text-as-button + delete, then the filter chips. Every interactive control has an `aria-label`. Focus ring uses a high-contrast blue. Touch targets are ≥ 44px. Color contrast on text is verified ≥ 4.5:1 (WCAG AA).
- **Test hook** — `window.__SAANJHA__` exposes `materializeList`, `getItems()`, `getState()`, and a `version` tag so the Playwright suite and future session-2 wiring can introspect without touching DOM internals.

## What's deliberately NOT here yet

| Concern | Session |
| --- | --- |
| Fabric SDK (vault.read/append/subscribe), FIF unlock | 2 |
| Real ULIDs from `ulidx` (current uses a pseudo-ulid for mock data) | 2 |
| Multi-list switcher + metadata edit | 3 |
| `fractional-indexing` library + drag-to-reorder | 3 |
| Operator menu (lists / settings / sync status / members / conflicts) | 4 |
| .naklilist export / import | 4 |
| Push-to-CDN deploy | 5 / M9 |

## Operational notes

- Deploy target: `list.naklitechie.com` (Cloudflare Pages) — M8 session 5 / M9.
- No backend other than the user's chosen Private Mesh transport (Hub, CF Worker, or Local Network).
- No analytics, no telemetry, no phone-home.

## Security notes

- FIF unlock state will live in memory only (session 2). No `localStorage` / `sessionStorage`.
- Tool-local settings will use IndexedDB (session 3+).
- All data on the wire and at rest in transport storage is encrypted client-side using fabric primitives.

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
