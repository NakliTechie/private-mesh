# fabric-sdk-go

Go SDK for the NakliTechie Private Mesh fabric protocol.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real APIs land in M1 (crypto + types) and M3 (conformance suite).

## Build

`go build ./...` once M1 lands. M0 has no Go sources yet — just the smoke test.

## Test

```sh
./smoke.sh   # M0: prints OK
```

## Configuration

None at M0. SDK consumers will pass transport endpoints and FIF unlock material at runtime.

## Security notes

This SDK will hold key material in memory during operations. Per the spec set, no key material is ever written to disk in plaintext. See [the security notes in the wire protocol](../docs/specs/fabric-spec-001-v1.0.md) and the [SDK spec](../docs/specs/fabric-sdk-go-spec-001-v1.1.md).

## Roadmap

- M1: crypto helpers (HKDF, Argon2id, ChaCha20-Poly1305), FIF parse/encrypt, macaroon mint/verify
- M3: conformance suite (32 tests)
- M5.5: bridge adapter framework + 8 starter adapters

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
