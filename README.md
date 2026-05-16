# Private Mesh

NakliTechie Private Mesh — a sovereign, browser-native, agent-aware capability fabric.

**Status:** Phase 1 — M0 skeleton (alpha).

## What this is

Seven primitives (Identity, Grant, Vault, History, Sync, LLM, Bridge), three transports (Hub, Cloudflare Worker, Local Network), two SDKs (Go and JavaScript), one CLI, one consumer tool (shared list).

The full vision, locked decisions, and complete spec set live under [`docs/`](docs/).

## Canonical documents

- [Vision](docs/vision-v0.7.md) — what this is, who it's for, why this shape
- [Decisions](docs/decisions-v0.7.md) — every locked decision with rationale
- [Specs](docs/specs/) — wire protocol, SDK specs, transport specs, consumer specs
- [Agent handoff](docs/specs/agent-handoff-fabric-v1.2.md) — Phase 1 implementation playbook
- [STATUS.md](STATUS.md) — milestone progress log

The wire protocol [`fabric-spec-001-v1.0.md`](docs/specs/fabric-spec-001-v1.0.md) is the contract. Everything else implements or consumes it.

## Repository layout

| Path | Contents |
| --- | --- |
| [`fabric-sdk-go/`](fabric-sdk-go/) | Go SDK |
| [`fabric-sdk-js/`](fabric-sdk-js/) | JavaScript SDK |
| [`fabric-merge-helpers/`](fabric-merge-helpers/) | Companion JS library |
| [`nakli-hub/`](nakli-hub/) | Hub binary (canonical transport) |
| [`nakli-cf-worker/`](nakli-cf-worker/) | Cloudflare Worker transport |
| [`nakli-local-bridge/`](nakli-local-bridge/) | mDNS bridge for browser tools |
| [`nakli-cli/`](nakli-cli/) | Reference CLI |
| [`saanjha/`](saanjha/) | Shared-list consumer tool (working name) |
| [`scripts/`](scripts/) | Build, conformance, release scripts |
| [`docs/`](docs/) | Vision, decisions, specs |

## Build

```sh
./scripts/build-all.sh
```

In M0 this runs each subdirectory's `smoke.sh` and prints `OK`. Real builds arrive at later milestones.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
