# fabric-sdk-js

JavaScript SDK for the NakliTechie Private Mesh fabric protocol. Runs in browsers (WebCrypto first, noble fallback) and in Node.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real APIs land in M1 (crypto + types) and M5 (full SDK with IndexedDB queue, Web Locks leader election, SSE subscribe).

## Build

TBD at M1. M0 has no JS sources yet.

## Test

```sh
./smoke.sh   # M0: prints OK
```

Headless-browser behavioral tests (Chromium, WebKit, Firefox) arrive at M5.

## Configuration

None at M0. SDK consumers configure transports and pass FIF unlock material at runtime.

## Security notes

WebCrypto is preferred for all symmetric and KDF operations. FIF material is held in memory only; the SDK never writes to `localStorage` or `sessionStorage`. See the [SDK spec](../docs/specs/fabric-sdk-js-spec-001-v1.1.md).

## Roadmap

- M1: crypto helpers, FIF parse/decrypt, macaroon mint/verify — interop-tested against the Go SDK
- M5: full SDK with IndexedDB queue, multi-tab leader election, SSE subscribe
- M5.5: bridge adapter framework

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
