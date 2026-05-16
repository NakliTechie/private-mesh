# nakli-hub

Hub binary — the canonical Private Mesh transport. Runs on the user's anchor (a small always-on machine, typically self-hosted) and serves the fabric protocol over HTTP.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real binary lands in M2. Once built:

```sh
nakli-hub init
nakli-hub serve
curl http://localhost:7842/fabric/v1/health
```

## Build

TBD at M2.

## Test

```sh
./smoke.sh   # M0: prints OK
```

Conformance suite (32 tests) lands in M3 and must pass for every release.

## Configuration

Will be config-file plus env-var driven. Defaults documented at M2.

## Operational notes

- Binary self-deploys via `curl|bash` (per D10)
- systemd unit on Linux, launchd plist on macOS
- `nakli-hub backup` / `restore` for state migration
- SQLite for state; `operation_log` retained ≥ 90 days; idempotency keys ≥ 24 h

## Security notes

The Hub stores only ciphertext payloads and macaroon-protected metadata. It enforces Grant attenuation on every request. It does NOT have access to FIF material; it only holds public Identity records and the operation log.

## Roadmap

- M2: HTTP handlers for all protocol endpoints, macaroon middleware, idempotency middleware, operation log, pairing
- M3: conformance suite
- M9: reproducible builds, GPG signing, `curl|bash` installer

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
