# nakli-cf-worker

Cloudflare Worker implementation of the Private Mesh fabric transport. The smallest of the three transports — a single TypeScript file plus Wrangler config.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real implementation lands in M6 once the protocol surface stabilizes and the conformance suite is ready.

## Build

TBD at M6. Will use Wrangler.

## Test

```sh
./smoke.sh   # M0: prints OK
```

Conformance suite (32 tests) must pass against a deployed Worker.

## Configuration

R2 for blob storage, KV for indexes. Wrangler config defines bindings. No secrets in the Worker itself; macaroon verification uses public verification keys distributed at pairing time.

## Operational notes

- Deployment via Wrangler to the operator's own Cloudflare account
- The operator pays Cloudflare; NakliTechie never sees this traffic
- Useful as fallback when the anchor is unreachable

## Security notes

Worker sees only ciphertext payloads. Macaroon verification gates every state-changing request. The Worker has no special privileges over user FIFs — it never sees them.

## Roadmap

- M6: full Worker implementation against the protocol; deploy + conformance pass
- M9: deployment automation

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
