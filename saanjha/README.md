# saanjha

Shared-list consumer tool (working name; final name TBD at launch). Phase 1's flagship consumer of the Private Mesh: a single-HTML, zero-account, multi-user list that syncs across devices using fabric History streams.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

The real tool lands in M8 across five sessions:
1. Skeleton + read-only with mock data
2. Fabric integration (single list)
3. Multi-list + UX polish
4. Operator surface + conflict UI
5. Polish + a11y + ship

## Build

TBD at M8. **No bundler.** Single HTML file is the deliverable; dependencies vendored or loaded from CDN.

## Test

```sh
./smoke.sh   # M0: prints OK
```

Manual user validation across browsers/devices/offline scenarios at M8 gate.

## Configuration

End-user configuration is via the in-tool setup flow — no edit-the-file step.

## Operational notes

- Deployed as a static HTML page to Cloudflare Pages (working URL: `list.naklitechie.com`)
- No backend other than the user's chosen Private Mesh transport
- No analytics, no telemetry, no phone-home
- Works offline; queues operations and flushes when reconnected

## Security notes

This tool holds FIF unlock state in memory only. State persistence uses **IndexedDB** (no `localStorage` / `sessionStorage`). All data on the wire and at rest in transport storage is encrypted client-side using fabric primitives.

## Roadmap

- M8: full tool per [shared-list-spec-001-v1.0.md](../docs/specs/shared-list-spec-001-v1.0.md)
- M9: deployment to `list.naklitechie.com`

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
