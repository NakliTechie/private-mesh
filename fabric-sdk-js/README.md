# fabric-sdk-js

JavaScript SDK for the NakliTechie Private Mesh fabric protocol. Runs in browsers (WebCrypto first, noble-ciphers + hash-wasm fallback for XChaCha20-Poly1305 and Argon2id) and in Node 22+.

**Status:** alpha (M1 — crypto + identity + grant primitives in place; full SDK ships at M5 with IndexedDB queue, multi-tab leader election, SSE subscribe.)

## Package name

`@naklitechie/fabric-sdk` on npm (decoupled from the in-repo path). Distributed as ESM; no bundler required by consumers (single-HTML deployment is a hard requirement for consumer tools per the NakliTechie shape).

## Quick start

```sh
pnpm install
pnpm test         # runs the M1 unit suite
./smoke.sh        # same, callable from build-all.sh
```

## Sources (M1)

- `src/crypto.js` — XChaCha20-Poly1305 (via `@noble/ciphers`), HKDF-SHA256 (WebCrypto), Argon2id (via `hash-wasm`)
- `src/identity/fif.js` — FIF envelope parse, decrypt, serialize; refuses reserved `envelope_type` values with `fif_envelope_unsupported`
- `src/grant/macaroon.js` — Macaroon mint/parse/verify wrapper over `macaroon` npm (wire-compatible with `gopkg.in/macaroon.v2`)
- `src/util/base64.js` — Standard-padded base64 helpers matching Go's `encoding/json` default for `[]byte`
- `interop/cli.js` — CLI used by `scripts/m1-interop.sh` for cross-SDK gate

## Build

No build step in M1. M5 adds the single-file ESM bundle for CDN distribution.

## Test

```sh
pnpm test
```

Node 22's built-in test runner. Headless-browser behavioral tests (Chromium, WebKit, Firefox) arrive at M5.

## Security notes

- WebCrypto is preferred for all symmetric and KDF operations available natively
- FIF material is held in memory only; the SDK never writes to `localStorage` or `sessionStorage`
- IndexedDB / OPFS are the only allowed persistence layers in consumer tools
- `FIF.lock()` zeroes cached envelope key material

## Roadmap

- M5: full SDK — IndexedDB queue, multi-tab leader election via Web Locks, SSE subscribe, transport manager, freshness/health APIs
- M5.5: bridge adapter framework + 8 starter adapters

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
