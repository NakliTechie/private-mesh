# Cross-SDK interop tests

This directory holds shared test vectors and generated fixtures for verifying
that `fabric-sdk-go` and `fabric-sdk-js` agree on wire-level artifacts.

Patches to a primitive (cryptographic envelope, on-wire format, macaroon
mint/verify, FIF parse/serialize, vault/history event encoding, etc.) MUST
extend these gates — see
[CONTRIBUTING.md § Interop gate for primitives](../CONTRIBUTING.md#interop-gate-for-primitives)
for the policy.

## Layout

| Path | Contents | Tracked? |
| --- | --- | --- |
| `m1-vectors.json` | M1 shared vectors — passphrase, principal, macaroon root key, identifier, caveats. | Yes |
| `m1/from-go/` | Fixtures written by the Go SDK. JS verifies them. | No (gitignored; regenerated) |
| `m1/from-js/` | Fixtures written by the JS SDK. Go verifies them. | No (gitignored; regenerated) |

## Gates

### `scripts/m1-interop.sh` — round-trip gate

```sh
./scripts/m1-interop.sh
```

Four phases:

1. Go writes `m1/from-go/fif.bin` and `m1/from-go/macaroon.bin` using the shared vectors.
2. JS reads `m1/from-go/*` and verifies (FIF unlocks; macaroon signature verifies).
3. JS writes `m1/from-js/fif.bin` and `m1/from-js/macaroon.bin`.
4. Go reads `m1/from-js/*` and verifies.

A green run prints `M1 interop: OK`.

### `scripts/m1-interop-nonce.sh` — AEAD nonce-rotation gate

```sh
./scripts/m1-interop-nonce.sh
```

Eight phases, exercising both directions:

1. Go writes baseline `from-go/fif.bin`.
2. JS re-serializes `from-go/fif.bin` → `from-go/fif-rot.bin` (must rotate the AEAD nonce).
3. Script asserts the nonces in the two files differ.
4. Go re-reads the JS-rotated file — proves the new nonce JS wrote is bound via AAD when Go parses.
5–8. Symmetric: JS writes, Go re-serializes, JS verifies.

A green run prints `M1 nonce interop: OK`.

This gate exists because `FIF.Serialize` previously reused the nonce stored at `NewFIF` time on every save — a catastrophic XChaCha20-Poly1305 misuse that two snapshots of an evolving FIF would expose. Any future change to FIF serialize must keep this gate green.
