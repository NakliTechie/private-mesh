# Cross-SDK interop tests

This directory holds shared test vectors and generated fixtures for verifying
that `fabric-sdk-go` and `fabric-sdk-js` agree on wire-level artifacts.

## Layout

| Path | Contents | Tracked? |
| --- | --- | --- |
| `m1-vectors.json` | M1 shared vectors — passphrase, principal, macaroon root key, identifier, caveats. | Yes |
| `m1/from-go/` | Fixtures written by the Go SDK. JS verifies them. | No (gitignored; regenerated) |
| `m1/from-js/` | Fixtures written by the JS SDK. Go verifies them. | No (gitignored; regenerated) |

## How to run

```sh
./scripts/m1-interop.sh
```

The script runs four phases:

1. Go writes `m1/from-go/fif.bin` and `m1/from-go/macaroon.bin` using the shared vectors.
2. JS reads `m1/from-go/*` and verifies (FIF unlocks; macaroon signature verifies).
3. JS writes `m1/from-js/fif.bin` and `m1/from-js/macaroon.bin`.
4. Go reads `m1/from-js/*` and verifies.

A green run prints `M1 interop: OK`. Any failure aborts with the failing phase named.
