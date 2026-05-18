# saanjha → roster

The shared-list consumer that used to live at `private-mesh/saanjha/` was extracted on 2026-05-18 to its own repository and renamed `roster`:

- **Repo:** [NakliTechie/roster](https://github.com/NakliTechie/roster)
- **Local path (post-reorg layout):** `~/Code/naklios-universe/roster/`
- **Licence change:** Apache-2.0 → **AGPL-3.0** (consistent with the wider Crate ecosystem)

The split is functional, not historical — `git log -- saanjha/` in this repo still has the full M8 development history, and the `STATUS.md` paragraphs from 2026-05-17 covering sessions 1–5 stay verbatim.

## What moved out of this repo

- `saanjha/saanjha.html` → `roster/roster.html` (single file, same architecture)
- `saanjha/smoke.sh` → `roster/smoke.sh`
- `saanjha/README.md` → `roster/README.md` (re-licensed to AGPL-3.0)

## What stayed (and was renamed)

The Playwright gates remain in this repo because they orchestrate `nakli-hub` + `nakli-cli` + `fabric-sdk-js` — all of which live here. They now pull `roster.html` from the sibling repo:

- `scripts/saanjha-gate.sh` → [`scripts/roster-gate.sh`](scripts/roster-gate.sh)
- `scripts/saanjha-fabric-gate.sh` → [`scripts/roster-fabric-gate.sh`](scripts/roster-fabric-gate.sh)
- `fabric-sdk-js/browser-test/saanjha-s{1,2,3,4}.spec.js` → `roster-s{1,2,3,4}.spec.js`

Both gates auto-detect the roster repo at the post-reorg layout (`../../roster`) and fall back to the legacy sibling layout (`../roster`). Override with `ROSTER_REPO=/absolute/path` if neither matches.
