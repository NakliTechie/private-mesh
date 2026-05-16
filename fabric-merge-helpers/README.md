# fabric-merge-helpers

Companion JavaScript library: merge / convergence helpers built on top of `fabric-sdk-js` for tools that maintain shared state in History streams.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real implementation arrives alongside M8 (Saanjha shared list) where these helpers are first consumed.

## Build

TBD. M0 has no sources.

## Test

```sh
./smoke.sh   # M0: prints OK
```

## Configuration

None at M0.

## Security notes

This library operates on plaintext payloads after the SDK has decrypted them. It never touches keys directly. See parent [security notes in fabric-sdk-js](../fabric-sdk-js/README.md#security-notes).

## Roadmap

- M8: shared-list merge helpers (LWW with vector clocks, conflict surface for UI)

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
