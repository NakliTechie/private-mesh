#!/usr/bin/env bash
# local-network-gate.sh — M7 gate: two Hubs + the bridge daemon on localhost
# discover each other via mDNS; the bridge surfaces both Hubs at GET
# /local/peers; one Hub pushes an event and the other pulls it via
# /sync/push + /sync/pull.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-ln-gate.XXXXXX)
trap '
  ec=$?
  [[ -n "${hub_a_pid:-}" ]] && kill "$hub_a_pid" 2>/dev/null || true
  [[ -n "${hub_b_pid:-}" ]] && kill "$hub_b_pid" 2>/dev/null || true
  [[ -n "${bridge_pid:-}" ]] && kill "$bridge_pid" 2>/dev/null || true
  if [[ $ec -ne 0 && -d "$tmp" ]]; then
    echo "--- gate exited $ec; dumping logs ---"
    for f in "$tmp"/hub-a.log "$tmp"/hub-b.log "$tmp"/bridge.log; do
      [[ -f "$f" ]] && { echo "===== $(basename "$f") ====="; tail -50 "$f"; }
    done
  fi
  rm -rf "$tmp"
' EXIT

hub_a_data="$tmp/hub-a"
hub_b_data="$tmp/hub-b"
cli_a_config="$tmp/cli-a/config.toml"
cli_b_config="$tmp/cli-b/config.toml"
grant_a="$tmp/cli-a/sync.macaroon"

# Free ports.
port_a=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17801)
port_b=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17802)
bridge_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17849)

echo "==> Building binaries"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)
(cd nakli-cli && go build -o "$tmp/nakli-cli" ./cmd/nakli-cli)
(cd nakli-local-bridge && go build -o "$tmp/nakli-local-bridge" ./cmd/nakli-local-bridge)

echo "==> Initializing two Hubs"
"$tmp/nakli-hub" init --data-dir "$hub_a_data" >"$tmp/hub-a-init.log"
"$tmp/nakli-hub" init --data-dir "$hub_b_data" >"$tmp/hub-b-init.log"

echo "==> Starting Hub A on 127.0.0.1:${port_a}"
"$tmp/nakli-hub" serve \
  --config "$hub_a_data/config.json" \
  --listen "127.0.0.1:${port_a}" \
  --local-network \
  >"$tmp/hub-a.log" 2>&1 &
hub_a_pid=$!
sleep 2

echo "==> Starting Hub B on 127.0.0.1:${port_b}"
"$tmp/nakli-hub" serve \
  --config "$hub_b_data/config.json" \
  --listen "127.0.0.1:${port_b}" \
  --local-network \
  >"$tmp/hub-b.log" 2>&1 &
hub_b_pid=$!
sleep 2

echo "==> Starting nakli-local-bridge on 127.0.0.1:${bridge_port}"
"$tmp/nakli-local-bridge" --listen "127.0.0.1:${bridge_port}" --announce-port "$bridge_port" >"$tmp/bridge.log" 2>&1 &
bridge_pid=$!
sleep 1

# Wait for both Hubs to be reachable.
for url in "http://127.0.0.1:${port_a}" "http://127.0.0.1:${port_b}" "http://127.0.0.1:${bridge_port}"; do
  for _ in $(seq 1 50); do
    if curl -fsS "${url}/fabric/v1/health" >/dev/null 2>&1 || curl -fsS "${url}/local/health" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
done

# Give mDNS more time to converge — macOS mDNSResponder can take 5-15s to
# propagate when multiple processes register similar services on the same
# host.
echo "==> Waiting for mDNS to converge"
hub_a_id=$(python3 -c "import json; print(json.load(open('$hub_a_data/hub-identity.json'))['hub_id'])")
hub_b_id=$(python3 -c "import json; print(json.load(open('$hub_b_data/hub-identity.json'))['hub_id'])")
for _ in $(seq 1 60); do
  count=$(curl -fsS "http://127.0.0.1:${bridge_port}/local/peers" 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['peers']))" 2>/dev/null || echo 0)
  if [[ "$count" -ge 2 ]]; then
    break
  fi
  sleep 1
done
peers_json=$(curl -fsS "http://127.0.0.1:${bridge_port}/local/peers")
echo "$peers_json" | python3 -m json.tool | head -50
peer_count=$(echo "$peers_json" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['peers']))")
if [[ "$peer_count" -lt 2 ]]; then
  echo "FAIL: bridge saw only $peer_count peer(s); expected ≥ 2"
  echo "--- bridge log ---"; tail -20 "$tmp/bridge.log"
  echo "--- hub-a log ---"; tail -20 "$tmp/hub-a.log"
  echo "--- hub-b log ---"; tail -20 "$tmp/hub-b.log"
  exit 1
fi

# Cross-check: each Hub also sees the other via /sync/peers.
echo "==> nakli-cli init against Hub A + mint sync grant"
printf 'pass-ln-gate\n' | "$tmp/nakli-cli" \
  --config "$cli_a_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "LN Gate A" \
       --fif "$tmp/cli-a/identity.fif" \
       --hub-url "http://127.0.0.1:${port_a}" \
       --hub-data-dir "$hub_a_data" >/dev/null
"$tmp/nakli-cli" --config "$cli_a_config" grant mint \
  --recipient "01J0LNGATESYNCRECIPIENT00001" \
  --primitive sync --namespace "*" --operations read,pull,push,write \
  --output "$grant_a" >/dev/null
grant_a_b64=$(tr -d '\n' < "$grant_a")

curl -fsS -H "X-Fabric-Grant: ${grant_a_b64}" "http://127.0.0.1:${port_a}/fabric/v1/sync/peers" | python3 -m json.tool | head -30
hub_a_sees=$(curl -fsS -H "X-Fabric-Grant: ${grant_a_b64}" "http://127.0.0.1:${port_a}/fabric/v1/sync/peers" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['peers']))")
if [[ "$hub_a_sees" -lt 1 ]]; then
  echo "FAIL: Hub A's /sync/peers saw zero peers; expected ≥ 1 (Hub B should be visible)"
  exit 1
fi

echo "==> Hub A appends a vault event, then pushes it to Hub B via /sync/push"
vault_grant="$tmp/cli-a/vault.macaroon"
"$tmp/nakli-cli" --config "$cli_a_config" grant mint \
  --recipient "01J0LNGATEVAULTRECIPIENT0001" \
  --primitive vault --namespace "*" --operations read,write \
  --output "$vault_grant" >/dev/null
vault_grant_b64=$(tr -d '\n' < "$vault_grant")
stream_id=$(python3 -c 'import secrets; print(secrets.token_hex(13).upper())')
event_json=$(curl -fsS -X POST \
  -H "X-Fabric-Grant: ${vault_grant_b64}" \
  -H "X-Fabric-Idempotency-Key: $(python3 -c 'import secrets; print(secrets.token_hex(13).upper())')" \
  -H "Content-Type: application/json" \
  -d "{\"namespace\":\"list\",\"stream_id\":\"${stream_id}\",\"event\":{\"kind\":\"ln-gate\",\"payload_ciphertext\":\"$(printf 'hello' | base64)\"}}" \
  "http://127.0.0.1:${port_a}/fabric/v1/vault/append")
event_id=$(echo "$event_json" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['event_id'])")
echo "  Hub A wrote event_id=$event_id"

# Pull from Hub A (since=0) and push to Hub B.
pull_json=$(curl -fsS -H "X-Fabric-Grant: ${grant_a_b64}" "http://127.0.0.1:${port_a}/fabric/v1/sync/pull?since=0&limit=50")
event_count=$(echo "$pull_json" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['events']))")
if [[ "$event_count" -lt 1 ]]; then
  echo "FAIL: Hub A /sync/pull returned zero events"
  echo "$pull_json"
  exit 1
fi
events_for_push=$(echo "$pull_json" | python3 -c "import sys,json; print(json.dumps({'events': json.load(sys.stdin)['data']['events']}))")

# Hub B's grant for sync push.
printf 'pass-ln-gate-b\n' | "$tmp/nakli-cli" \
  --config "$cli_b_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "LN Gate B" \
       --fif "$tmp/cli-b/identity.fif" \
       --hub-url "http://127.0.0.1:${port_b}" \
       --hub-data-dir "$hub_b_data" >/dev/null
grant_b="$tmp/cli-b/sync.macaroon"
"$tmp/nakli-cli" --config "$cli_b_config" grant mint \
  --recipient "01J0LNGATESYNCRECIPIENT00002" \
  --primitive sync --namespace "*" --operations read,pull,push,write \
  --output "$grant_b" >/dev/null
grant_b_b64=$(tr -d '\n' < "$grant_b")

push_json=$(curl -fsS -X POST \
  -H "X-Fabric-Grant: ${grant_b_b64}" \
  -H "X-Fabric-Idempotency-Key: $(python3 -c 'import secrets; print(secrets.token_hex(13).upper())')" \
  -H "Content-Type: application/json" \
  -d "$events_for_push" \
  "http://127.0.0.1:${port_b}/fabric/v1/sync/push")
echo "  push → $(echo "$push_json" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f\"accepted={d.get('accepted')} skipped={d.get('skipped')}\")")"
accepted=$(echo "$push_json" | python3 -c "import sys,json; print(json.load(sys.stdin)['data'].get('accepted', 0))")
if [[ "$accepted" -lt 1 ]]; then
  echo "FAIL: Hub B /sync/push accepted zero events"
  echo "$push_json"
  exit 1
fi

# Verify Hub B has the event by pulling it back via /sync/pull on Hub B's
# own Grant. (Grants are issued per-Hub; vault_grant_b64 was minted against
# Hub A and won't verify on Hub B — cross-Hub Grant trust is M7.x.)
pulled=$(curl -fsS -H "X-Fabric-Grant: ${grant_b_b64}" "http://127.0.0.1:${port_b}/fabric/v1/sync/pull?since=0&limit=50")
echo "$pulled" | python3 -m json.tool | head -30
if ! echo "$pulled" | grep -q "$event_id"; then
  echo "FAIL: Hub B's /sync/pull does not contain the synced event $event_id"
  exit 1
fi

echo "==> local-network-gate done — M7 gate green"
