# fabric-sdk-go

Go SDK for the NakliTechie Private Mesh fabric protocol.

**Status:** alpha (M1 — crypto + identity + grant primitives in place; transports and primitive clients land at M2–M5.5)

## Module path

`github.com/NakliTechie/private-mesh/fabric-sdk-go`. The SDK lives in the monorepo subdirectory so `go get` resolves directly without vanity-import setup. The Go SDK spec's standalone `github.com/naklitechie/fabric-sdk-go` path is a publishing alias decision deferred to release.

## Quick start

```sh
./smoke.sh        # runs go test ./...
go test ./...     # full unit suite
go run ./cmd/interop -mode=generate -dir ../interop-tests/m1   # write interop fixtures
```

## Packages (M1)

- `crypto/` — XChaCha20-Poly1305 AEAD, HKDF-SHA256, Argon2id wrappers
- `identity/` — Fabric Identity File (FIF) envelope parse, decrypt, serialize; refuses reserved envelope_type values with `fif_envelope_unsupported` per the forward-compat hook
- `grant/` — Macaroon mint/parse/verify wrapper over `gopkg.in/macaroon.v2`

## Build

```sh
go build ./...
```

## Test

```sh
go test ./...
```

## Configuration

None at the SDK boundary. Consumers configure transports and pass FIF unlock material at runtime.

## Security notes

- All payload encryption uses XChaCha20-Poly1305 with HKDF-SHA256 per-namespace key derivation
- FIF envelope binds the on-wire header bytes as AAD so a tampered header invalidates the MAC
- Macaroon HMAC chain is the authorization root; no key material is ever written to disk by the SDK
- `FIF.Lock()` zeroes cached envelope key material

## Roadmap

- M2: Hub binary embeds this SDK for client-side operations + macaroon verification middleware
- M3: conformance suite (32 tests) lives under `conformance/`
- M5.5: bridge adapter framework + 8 starter adapters

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).

