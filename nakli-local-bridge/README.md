# nakli-local-bridge

Standalone mDNS bridge daemon. Lets browser-based consumer tools participate in the Local Network transport (mDNS) which browsers cannot speak directly.

**Status:** alpha — **M7 complete (discovery half).** Announces on `_nakli-fabric._tcp.local.`, browses for other fabric peers, and exposes a small HTTP surface at `http://127.0.0.1:7849/local/peers` that browser tools consume. WebRTC signaling relay (`POST /local/signal`) and the WebSocket peer-list observer (`/local/peers/observe`) ship 501 stubs in M7 and land at M7.x.

## Quick start

```sh
./smoke.sh                            # build + report
./nakli-local-bridge --verbose        # run, prints discovered peers as they arrive
```

End-to-end with two Hubs:

```sh
../scripts/local-network-gate.sh      # builds + runs two Hubs + bridge, syncs an event
```

## Surface

| Endpoint | Status |
| --- | --- |
| `GET /local/health` | Returns `{binary, version, announcing, instance, port}` |
| `GET /local/peers` | Returns currently-discovered peers (the fabric ones, not the bridge itself) |
| `POST /local/signal` | **501** — WebRTC offer/answer/ICE relay lands at M7.x |
| `WS  /local/peers/observe` | **501** — live peer-list streaming lands at M7.x |

`GET /local/peers` payload mirrors the spec (cf-worker-spec-001-v1.1.md §"GET /local/peers"):

```json
{
  "ok": true,
  "data": {
    "peers": [
      {
        "transport_id": "01HMX...",
        "principal_id": "01HMX...",
        "hub_id": "01HMX...",
        "host": "anchor.local",
        "port": 7842,
        "url": "http://192.168.1.42:7842",
        "version": "naklimesh/1.0",
        "capabilities": ["vault", "history", "sync", "grant", "identity"],
        "discovered_at": "2026-05-17T12:34:56Z",
        "last_seen_at":  "2026-05-17T12:35:01Z"
      }
    ]
  }
}
```

## Flags

```
nakli-local-bridge [--listen 127.0.0.1:7849] \
                    [--announce] [--instance nakli-local-bridge] \
                    [--announce-port 7849] [--verbose]
```

The bridge announces itself as a discovery-only peer (`capabilities=discovery`) and excludes itself from `/local/peers` results.

## Build

```sh
go build ./cmd/nakli-local-bridge
```

Single-file `main.go` (≤160 LOC); the heavy lifting lives in [`fabric-sdk-go/local`](../fabric-sdk-go/local/local.go) so the Hub can embed the same Announcer + Browser.

## Operational notes

- Runs as a user-level daemon. Launchd plist + systemd unit land at M9.
- LAN-only by design — does not bridge to remote networks.
- No persistence required; discovers peers fresh each session.

## Security notes

- The bridge sees ciphertext only. It does not hold FIF material or macaroon keys.
- The bridge announces its own presence on mDNS so other peers can confirm it's reachable for WebRTC signaling. Disable with `--announce=false` if you only want discovery without being visible.
- Browser tools should treat the bridge as untrusted middleware: the protocol Grants still gate every operation; the bridge just connects browsers to peers.

## Roadmap

- M7 (done): mDNS announce + browse; HTTP `/local/peers`
- M7.x: WebRTC signaling relay, WebSocket peer-list streaming, mDNS challenge/response
- M9: signed releases, service-unit templates

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
