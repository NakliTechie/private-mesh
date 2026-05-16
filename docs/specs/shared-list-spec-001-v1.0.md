# Shared List Specification

**Document:** `shared-list-spec-001-v1.0.md`
**Status:** v1.0 draft, normative
**Companion to:** `fabric-spec-001-v1.0.md`, `fabric-sdk-js-spec-001-v1.0.md`
**Audience:** Implementers of the shared-list tool; coding agents producing v1.0; reviewers.

The shared list is the **first fabric-native tool** (D6). Phase 1's flagship consumer. It exists for one reason: to validate that the Private Mesh platform works end-to-end under real multi-user pressure. A household grocery list is the smallest possible scenario where the failure model, the freshness model, the conflict model, and the agent-aware model all matter at once.

Per the vision doc, the platform is "fully proven by the moment shared-list works convincingly for a household of 3-5 people across browsers, devices, and intermittent connectivity." This spec defines what "works convincingly" means.

**Tool name** (deferred per standing rule): TBD at launch. The internal codename is "Saanjha" (Hindi: "shared, joint, communal") but the launch name is decided closer to ship. Spec uses "the list" throughout.

---

## Scope

This document specifies:
- Tool purpose, audience, and shape
- Functional requirements (what users can do)
- Data model (events and merge semantics)
- UX requirements
- File format (`.naklilist` for offline export)
- Conflict surface (operator + participant)
- Integration with Fabric SDK
- Agent affordances (per D-Agents)
- Build/deploy constraints
- Out-of-scope for v1.0

---

## Tool shape

- Single HTML file (`saanjha.html`, ~50-150 KB inlined CSS/JS)
- Imports `fabric-sdk-js` and `fabric-merge-helpers` from CDN (or vendored)
- Zero build step
- Zero account
- Zero telemetry
- BYOK never persisted (Bridge unused in v1.0 of this tool)
- FSA-first persistence (the list itself lives in Fabric; tool-local settings in OPFS or IndexedDB)
- Works as a Cloudflare Pages site OR opened from filesystem (file:// — but with limitations because Fabric needs HTTPS for some transport modes)
- Recommended deployment: `list.naklitechie.com` (Cloudflare Pages)

---

## Audience

Following the dual-audience pattern:

**The operator** (one person per household typically):
- Set up the Fabric transports
- Add household members as principals (via pairing)
- Mint Grants for each member to read/write the list namespace
- Knows about the queue, freshness, conflicts

**The participants** (everyone else in the household):
- Open the page on phone/laptop
- See "the list"
- Add things, check off things
- May not even know the word "Fabric" exists
- "It just works"

Both must be first-class. The participant UI is the default; the operator surface is available on demand.

---

## Functional requirements

### Core operations

The tool MUST support:
- View the current list (items, with check-state and quantity)
- Add an item (text + optional quantity)
- Check off an item (mark as obtained)
- Uncheck an item
- Delete an item (with confirmation)
- Edit an item's text or quantity
- Reorder items (drag handle on touch; arrows on keyboard)
- Filter: show all / show open / show checked
- Multiple lists (one tool, many lists — "groceries", "hardware store", "for the trip")

### Multi-user operations

- See who added an item (optional; displayed as initials or small avatar)
- See "Bhai is editing..." indicator briefly (no real-time presence in v1.0; just a hint when SDK events show concurrent activity)
- Handle two people adding the same item near-simultaneously without losing either
- Handle two people checking off the same item without flicker

### Offline operations

- Add/check/edit items while offline; they queue
- Show "X pending sync" indicator when queue is non-empty
- On reconnect, queue drains; queued items appear normally to everyone
- If a queued operation fails permanently (rare), surface clearly to the user

### Conflict handling

- When two people edit the same item concurrently: both edits persist (event-sourced); the tool resolves with last-write-wins-per-field within a vector-clock-causally-known set
- When two people delete and edit the same item concurrently: deletion wins, with a 5-second undo offered to the editor
- When checking off the same item concurrently: idempotent (both events produce the same effective state)

The tool MUST never silently lose user input. If a conflict resolution would discard an edit, surface it as "your edit may not have applied; check 'X'."

---

## Data model

The list lives in a Fabric Vault stream:

```
namespace: "saanjha"
stream_id: ULID per list, e.g., "01HMXL..."
```

Stream metadata (stored as `list:metadata` event, replaced on rename):
```json
{
  "kind": "list:metadata",
  "payload": {
    "name": "Groceries",
    "color": "#7c9a8a",
    "icon": "shopping-cart",
    "created_at": "<rfc3339>",
    "created_by_principal": "<ulid>"
  }
}
```

### Event types

```typescript
type ListEvent =
  | { kind: "list:item-added"; payload: { item_id: string; text: string; qty?: string; position?: string; } }
  | { kind: "list:item-checked"; payload: { item_id: string; } }
  | { kind: "list:item-unchecked"; payload: { item_id: string; } }
  | { kind: "list:item-edited"; payload: { item_id: string; text?: string; qty?: string; } }
  | { kind: "list:item-reordered"; payload: { item_id: string; new_position: string; } }
  | { kind: "list:item-deleted"; payload: { item_id: string; } }
  | { kind: "list:metadata"; payload: { name?: string; color?: string; icon?: string; } };
```

`item_id` is a ULID generated client-side; ensures items don't collide across devices.

`position` is a fractional-indexing string (e.g., "a0", "a0V", "a1"); enables reordering without renumbering everything. Library: `fractional-indexing` (bundled, ~2 KB).

### Materialization

The current state of the list is computed by replaying events:

```typescript
function materializeList(events: ListEvent[]): ListState {
  const items = new Map<string, Item>();
  let metadata = defaultMetadata();
  
  for (const event of events) {
    switch (event.kind) {
      case "list:item-added":
        if (!items.has(event.payload.item_id)) {
          items.set(event.payload.item_id, { ...event.payload, checked: false });
        }
        break;
      case "list:item-checked":
        const item = items.get(event.payload.item_id);
        if (item) item.checked = true;
        break;
      case "list:item-deleted":
        items.delete(event.payload.item_id);
        break;
      // ... etc
    }
  }
  return { items: Array.from(items.values()).sort(byPosition), metadata };
}
```

Materialization is pure and deterministic given the same event sequence. This makes conflict resolution debuggable.

### Merge semantics

The list uses `fabric-merge-helpers.appendUnion()` semantics: events from concurrent writers are unioned. The materialization function above handles every event combination idempotently.

For the "two people edit same item" case:
- Both edits are events with vector clocks
- Materialization applies them in causal order
- When they're concurrent (no causal relation), the materializer uses last-write-wins per field, with vector-clock tie-breaking on (timestamp, device_id)
- The tool surfaces this to users via "you may have been edited" if the local edit's effect was overwritten

---

## File format: `.naklilist` (offline export)

Per Creative Suite pattern (each tool has its own format), the list exports to `.naklilist`:

```json
{
  "format": "naklilist/1.0",
  "exported_at": "<rfc3339>",
  "exported_by_principal": "<ulid>",
  "list": {
    "stream_id": "<ulid>",
    "namespace": "saanjha",
    "metadata": { ... },
    "events": [
      { "event_id": "<ulid>", "kind": "list:item-added", "payload": {...}, "appended_at": "..." }
    ]
  }
}
```

Use cases:
- Backup
- Share a list with someone NOT on your Fabric (they import; their changes are local)
- Archive a completed list

Import: reads the file, optionally creates a new stream and replays events (re-issued under the importer's principal — the original provenance is preserved in metadata).

---

## UX requirements

### Layout (mobile-first)

```
┌─────────────────────────────────────────┐
│ ≡ Groceries          14 open  • B  P  S│
├─────────────────────────────────────────┤
│ + Add an item...                        │
├─────────────────────────────────────────┤
│ ☐ Milk             2L                 ⠿│
│ ☐ Bread            atta               ⠿│
│ ☑ Onions           1kg                ⠿│
│ ☐ Tomatoes         500g               ⠿│
│ ☑ Cooking oil      1L                 ⠿│
│ ☐ Curd             400g               ⠿│
├─────────────────────────────────────────┤
│   Showing all (14)   filter: open  ↓   │
└─────────────────────────────────────────┘
```

- The hamburger menu opens the operator-level surface (lists, settings, sync status, conflicts)
- The dots on top-right are presence indicators (initials of recently-active members)
- Add input is sticky at top
- Each row: checkbox + text + qty + drag handle (touch) or move arrows (keyboard)
- Tap on text → inline edit
- Long press / right-click → context menu (delete, change qty, duplicate)
- Filter chip switches all/open/checked

### Visible-to-the-curious surface

Tap or hover the hamburger:
- "Lists" — switch between lists
- "Settings" — name, color, icon
- "Sync status" — freshness indicator, queue depth, "last synced X seconds ago"
- "Conflicts" — list of any open conflicts (rare)
- "Members" — who has access
- "Export this list (.naklilist)"
- "About — fabric version, transport status"

This surface uses the same SDK APIs (`fabric.freshness`, `fabric.queue`, `fabric.events`) and surfaces what's there.

### Tone

- Minimal, functional, calm
- No emoji in chrome (per overall NakliTechie tone)
- Indian English where natural (no "favorite color" forced, no Americanisms — "kindly" sparingly)
- Items support Devanagari and other scripts (UTF-8 throughout)

### Color and type

Per Rangrez palettes (per primitives layer plan):
- Default palette: TBD — uses a calm muted palette appropriate for a domestic context
- User can pick a palette per list
- Type: Inter Tight or system stack (per Slate handoff)

### Accessibility

- Full keyboard navigation
- Screen reader labels on every interactive element
- Minimum 4.5:1 contrast on text
- Touch targets ≥ 44px
- Color is not the only signal (checked state has both color and icon)

---

## Conflict surface

When the SDK emits a `conflict` event for this list's stream:

### For participants (default UI)
A small banner appears at the top of the list:
> "Some items have concurrent edits. Tap to review."

Tapping opens a focused view showing each conflicted item with both versions and a "keep mine / keep theirs / merge" choice for each. After resolution, the banner clears.

If the user dismisses without resolving, the banner persists across sessions.

### For operators (advanced)
The hamburger menu's "Conflicts" panel shows all open conflicts with full structured detail (vector clocks, event IDs, principals involved). Operator can resolve programmatically or via the participant UI.

### Resolution writes back

User's resolution emits a new `list:item-edited` event (or `list:item-deleted` if "discard both"), with `causal_dependencies` set to the conflicting events. Subsequent materializations see the resolution and treat it as authoritative.

---

## Fabric integration

### On load

```typescript
const fabric = new Fabric();
const fifBytes = await loadFIFFromFSA();  // or file picker
await fabric.unlockFIF(fifBytes, await promptPassphrase());
```

If FIF unlock fails or the user has no FIF, the tool shows a "Set up your identity" wizard linking to `nakli-cli init` or a future browser-based setup flow. v1.0 of this tool assumes the user already has a FIF.

### Reading the list

```typescript
const events = await fabric.vault.read("saanjha", currentListId, { limit: 1000 });
const state = materializeList(events);
render(state);

// Subscribe to live updates
const subscription = fabric.vault.subscribe("saanjha", currentListId);
for await (const event of subscription) {
  state = applyEvent(state, event);
  render(state);
}
```

### Writing to the list

```typescript
async function addItem(text: string, qty?: string) {
  const itemId = generateUlid();
  const position = nextPosition(state.items);
  
  // Optimistic local update
  state.items.set(itemId, { itemId, text, qty, position, checked: false });
  render(state);
  
  // Queue (SDK handles retry, idempotency)
  try {
    await fabric.vault.append({
      namespace: "saanjha",
      streamId: currentListId,
      event: {
        kind: "list:item-added",
        payload: { item_id: itemId, text, qty, position },
      },
    });
  } catch (err) {
    if (err.retryable) {
      // SDK has queued it; UI shows "pending sync" indicator
    } else {
      // Permanent failure (e.g., scope_denied); roll back local state
      state.items.delete(itemId);
      render(state);
      showError(`Could not add: ${err.message}`);
    }
  }
}
```

### Freshness display

```typescript
fabric.freshness.observe((snapshot) => {
  updateFreshnessIndicator(snapshot.stalenessMs, snapshot.peersSynced.length);
});
```

### Queue display

```typescript
fabric.queue.observe((event) => {
  if (event.type === "enqueued" || event.type === "succeeded") {
    updateQueueIndicator(fabric.queue.size());
  }
});
```

---

## Agent affordances (per D-Agents)

The list is one of the tools where agents-doing-work is a real use case:

- "Add everything we ran out of from last week" → an agent reads recent History (consumption events from a different tool), figures out what's depleted, adds to the list
- "Mark off what's already in the pantry" → agent looks at pantry inventory tool (future), unchecks items
- "Add 'milk' if Bhai didn't pick it up by 7pm" → agent reads list state, checks time, conditionally adds

For this tool specifically:

1. **No special agent UX** — agents use the same Fabric Vault endpoints as the human UI. The tool doesn't have an agent-specific interface; the SDK is the interface.
2. **History audit visible** — the "Members" panel shows which items were added by humans vs agents (using the appended_by_principal field; agents are tagged with their principal_type).
3. **Per-list agent toggle** — operator can mint a Grant for an agent scoped to a specific list (or all lists). The "Members" panel shows what each agent's Grant allows.
4. **Bridge calls not used** — this tool doesn't make external calls in v1.0. (No "auto-order from Instacart" — that's a v2 thought.)

### Showing who added an item

In the participant view, items added by agents show a small icon (a different shape from a human's initials, e.g., a circle vs a letter) and the agent's name appears on hover/long-press.

This is a key D-Agents commitment: provenance is legible to humans without being intrusive.

---

## Single-HTML constraints

Per NakliTechie shape:
- One `.html` file at deploy time (inlined CSS/JS, or `<script src>` references to bundled JS in the same directory)
- Zero build step required to develop (open in a browser)
- All third-party deps loaded from CDN with SRI hashes OR vendored
- No bundler config (no Webpack/Vite/Rollup) — though development MAY use Vite for hot reload, the deployed artifact is a single file
- Approximate sizes:
  - HTML+CSS+app JS: 30-50 KB
  - Fabric SDK JS (CDN): ~100 KB minified, ~30 KB gzipped
  - fabric-merge-helpers (CDN): ~10 KB
  - fractional-indexing (CDN): ~2 KB
  - Total wire: ~150 KB cold-start; <50 KB cached

---

## Deployment

- Repository: `naklitechie/saanjha` (placeholder name)
- Build: none required; the HTML is the artifact
- Hosting: Cloudflare Pages, deployed from `main` branch
- Custom domain: `list.naklitechie.com` (TBD final name)
- CI: lint, type-check (if TypeScript used internally), conformance test against a test Hub
- Releases: tagged commits → automatic Pages deploy

---

## Build sequence (per Creative Suite-style sessions)

Per Bhai's spec-first methodology, this tool is built in agent sessions. Suggested order:

**Session 1: Skeleton + read-only**
- HTML/CSS layout
- Mock data
- Materialization function
- Basic rendering
- No Fabric integration yet

**Session 2: Fabric integration**
- FIF unlock flow
- Subscribe to a single list's stream
- Display real events
- Add/check/delete write paths
- Basic freshness indicator

**Session 3: Multi-list + UX polish**
- List switcher
- Add/edit list metadata
- Drag-to-reorder
- Filter chip
- Inline edit

**Session 4: Operator surface + conflict UI**
- Hamburger menu
- Sync status panel
- Members panel with agent provenance
- Conflict resolution UI
- Export to .naklilist

**Session 5: Polish + ship**
- a11y audit and fixes
- Mobile UX (touch targets, drag, gestures)
- Loading and error states
- README, deploy
- Manual multi-device test

Each session: spec → agent codes → smoke test → commit. Standard Bhai workflow.

---

## Success criteria

Per the vision doc: "the platform is fully proven by the moment shared-list works convincingly for a household of 3-5 people across browsers, devices, and intermittent connectivity."

Operational definition of "works convincingly":

- 3-5 humans on the same list, on different devices (mix of iOS Safari, Chrome desktop, Android Chrome at minimum)
- One device goes offline; adds 3-4 items; comes back online → all items appear on others' devices within 5 seconds
- Two devices simultaneously add an item with the same text → both items appear (no false dedup)
- Two devices simultaneously check off the same item → final state is "checked" (idempotent)
- Two devices simultaneously edit the same item's text → conflict surface appears; user resolves; result is consistent everywhere
- One device's transport (Hub) goes offline; tool falls back to Cloudflare Worker; sync resumes; no data loss
- All three transports unreachable; queue persists; on reconnect, everything reconciles
- Agent provisions with read-only Grant; cannot add items even when asked
- Agent provisions with write Grant scoped to one list; cannot write to other lists
- Agent retired mid-session; queued operations from that agent fail with `grant_invalid`

If all of these work without manual fixup, the platform is proven.

---

## Out of scope for v1.0 of the tool

- Real-time presence (typing indicators, cursor positions) — v2 thought
- Voice input — separate tool concern (Bolo handles voice)
- Smart suggestions / auto-complete from past purchases — v2; needs a more sophisticated model
- Receipt-photo scanning to bulk-add items — Slate/Folio integration; v2
- Recurring items (auto-add "milk" every Monday) — v2; would need a scheduling primitive
- Categorization / aisle grouping — v2 polish; many UX paths
- Reminders / push notifications — out of scope for browser-tool model
- iOS Add-to-Home-Screen niceties (icon, splash, manifest.webmanifest) — required for v1.0 actually; trivial
- Native mobile app — never; we use browser as the platform
- Bridge to external services (Instacart, BlinkIt) — v2 thought, requires Bridge primitive use; v1.0 stays clean

---

## Out of scope for v1.0 of the platform (referenced)

These are features the tool COULD use but the platform doesn't yet provide:
- Push notifications on Fabric event → would need a service-worker integration with the Subscribe stream
- Cross-stream causal queries → would need a richer query primitive on Vault
- Anomaly detection on agent operations → v1.x

The tool ships without these; v2 of the tool revisits when the platform offers them.

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md`
- JS SDK spec: `fabric-sdk-js-spec-001-v1.0.md`
- Vision doc: `private-mesh-vision-001-v0.7.md`
- Decision D6 (shared list as first fabric-native tool)
- Decision D-Agents (agents-as-principals affordances)
- Decision D-Failure (the six hooks this tool exercises)
- fractional-indexing: https://github.com/rocicorp/fractional-indexing
