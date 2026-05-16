# nakli-local-bridge

Standalone mDNS bridge daemon. Lets browser-based consumer tools participate in the Local Network transport (mDNS) which browsers cannot speak directly.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real implementation lands in M7. Browser tools detect the bridge over a local websocket and use it as a transparent relay to other peers on the LAN.

## Build

TBD at M7.

## Test

```sh
./smoke.sh   # M0: prints OK
```

## Configuration

Bind address / port; mDNS service name; optional explicit peer list (for networks where mDNS is blocked).

## Operational notes

- Runs as a user-level daemon
- launchd on macOS, systemd on Linux (Windows: TBD)
- No persistence required; discovers peers fresh each session

## Security notes

The bridge sees ciphertext traffic only. It does not hold keys. It announces presence on the LAN and accepts connections from local processes (i.e. browser tools running on the same machine). LAN-only by design — does not bridge to remote networks.

## Roadmap

- M7: full bridge per [local-network-spec-001-v1.1.md](../docs/specs/local-network-spec-001-v1.1.md)

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
