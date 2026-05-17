# fabric-sdk-js

JavaScript SDK for the NakliTechie Private Mesh fabric protocol. Runs in browsers (WebCrypto first, `@noble/ciphers` + `hash-wasm` for XChaCha20-Poly1305 and Argon2id) and in Node 22+.

**Status:** alpha — **M5 complete.** Top-level `Fabric` class wired; Vault append/read with client-side encryption transparency; History with hash-chain bookkeeping; Grants (mint via Hub + local inspect); Transport manager; Freshness + Health hooks; EventBus; LLM/Bridge/Sync stubs. Cross-browser gate (Chromium + Firefox + WebKit) green via Playwright.

## Package name

`@naklitechie/fabric-sdk` on npm. Distributed as a single ESM file produced by esbuild (`dist/fabric-sdk.js` ~256 KB, `dist/fabric-sdk.min.js` ~140 KB) — no bundler required by consumers (single-HTML deployment is a hard requirement for consumer tools per the NakliTechie shape).

## Quick start

```sh
pnpm install
pnpm test          # Node unit suite (38 tests as of M5)
pnpm build         # produces dist/fabric-sdk.{js,min.js}
./smoke.sh         # same as pnpm test, callable from build-all.sh
```

End-to-end browser gate (builds Hub + CLI + SDK, runs Playwright):

```sh
../scripts/js-gate.sh                       # default project (all browsers)
PLAYWRIGHT_PROJECT=firefox ../scripts/js-gate.sh
```

## Top-level surface

```js
import { Fabric } from '@naklitechie/fabric-sdk';

const fabric = new Fabric({
  transports: [{ url: 'http://127.0.0.1:7842' }],
});
// Out-of-band: unlock a FIF or inject a paired root seed.
await fabric.unlockFIF(fifBytes, passphrase);
fabric.useGrant(macaroonB64);

// Vault — encryption is transparent.
await fabric.vault.append({
  namespace: 'list',
  streamId: 'shopping',
  event: { kind: 'list:item-added', payload: { item: 'milk' } },
});
const { events } = await fabric.vault.read('list', 'shopping');
```

| API | M5 status |
| --- | --- |
| `Fabric.unlockFIF` / `createFIF` / `lock` | implemented (createFIF takes a caller-supplied keypair; SDK Ed25519 keygen lands at M5.x) |
| `Fabric.useGrant` / `currentGrant` | implemented |
| `vault.append` / `vault.read` / `vault.listStreams` | implemented (with HKDF-derived namespace key + XChaCha20-Poly1305) |
| `vault.subscribe` (SSE) | deferred → M5.x |
| `history.append` / `read` / `verify` | implemented (auto-managed `previous_event_hash`) |
| `grants.mint` (via Hub) / `inspect` / `verify` / `revoke` | implemented |
| `grants.mintLocal` (test/bootstrap) | implemented |
| `transports.list/add/remove/current/switch` | implemented |
| `freshness.current` / `observe` | implemented |
| `health.current` / `observe` | implemented |
| `events.on/off/emit` | implemented |
| `sync` | thin call-through stubs (peers list); full sync → M7 |
| `llm.complete` / `routes` / `registerBrowserBackend` | thin stubs (Hub returns 501 in v1.0); full routing → M5+ |
| `bridge.call` / `approve` / `adapters` | thin call-through stubs; full adapters → M5.5 |
| Operation queue (IndexedDB) | deferred → M5.x |
| Web Locks leader election | deferred → M5.x |
| Pairing / agent provisioning UI flows | deferred → M5.x |
| Conformance suite hook | deferred → M5.x (uses fabric-sdk-go suite for now) |

## Cross-browser gate

[`../scripts/js-gate.sh`](../scripts/js-gate.sh) is the M5 gate: it builds `nakli-hub` + `nakli-cli` + the SDK bundle, starts the Hub on a free port, has the CLI mint a wildcard Grant + a random 32-byte root seed, serves `browser-test/pages/sandbox.html` on another free port, and runs Playwright. The sandbox page loads `dist/fabric-sdk.js`, constructs `Fabric`, performs `vault.append` then `vault.read`, and asserts the decrypted payload matches.

Verified on **Chromium 142**, **Firefox 145**, **WebKit 26.4**.

## Sources

- `src/index.js` — public API exports
- `src/fabric.js` — top-level `Fabric` class
- `src/crypto.js` — XChaCha20-Poly1305, HKDF-SHA256, Argon2id (carry-over from M1)
- `src/keys.js` — per-namespace vault key derivation (HKDF over the FIF root seed)
- `src/identity/fif.js` — FIF envelope parse / unlock / serialize (M1)
- `src/grant/macaroon.js` — macaroon wire helpers (M1)
- `src/grants.js` — `GrantStore` calling `/grant/mint|verify|revoke`
- `src/vault.js` — `VaultAPI` with encryption transparency
- `src/history.js` — `HistoryAPI` with hash-chain bookkeeping
- `src/stubs.js` — `SyncAPI`, `LLMAPI`, `BridgeAPI` thin call-throughs
- `src/transport.js` — `HubTransport` + `TransportManager`
- `src/events.js` — `EventBus`
- `src/freshness.js` / `src/health.js` — failure-model surfaces
- `src/errors.js` — typed error hierarchy
- `src/util/base64.js` — base64 helpers (M1)

## Build

```sh
pnpm build
```

Bundles via `esbuild` (browser target ES2022). The `util` module that `macaroon@3.0.4` imports for TextEncoder/TextDecoder fallback is aliased to a tiny stub that uses the browser globals (`scripts/util-stub.js`).

## Test

```sh
pnpm test                                  # Node unit suite
../scripts/js-gate.sh                      # end-to-end browser gate
PLAYWRIGHT_PROJECT=webkit ../scripts/js-gate.sh
```

## Security notes

- WebCrypto is preferred for all symmetric and KDF operations available natively
- FIF material is held in memory only; the SDK never writes to `localStorage` or `sessionStorage`
- Vault payloads are encrypted with a per-namespace key derived from the FIF's root keypair seed via HKDF-SHA256; the Hub only ever sees ciphertext (the M5 SDK gate exercises this round-trip in three browsers)
- `Fabric.lock()` zeroes cached envelope key material and the per-namespace vault key cache

## Roadmap

- M5 (done): top-level class, Vault/History, Grants, Transport, Freshness/Health, EventBus, esbuild bundle, cross-browser gate
- M5.x: IndexedDB operation queue, Web Locks leader election, SSE `vault.subscribe`, pairing flows, agent provisioning, FIF Ed25519 keygen
- M5.5: bridge adapter framework + 8 starter adapters
- M7: multi-anchor sync (sync.* APIs become live)

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
