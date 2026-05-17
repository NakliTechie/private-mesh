#!/usr/bin/env bash
# cli-gate.sh — exercise the M4 gate: init → vault append/read → conformance.
# Builds nakli-hub + nakli-cli, starts the Hub on a free port, runs the CLI
# end-to-end. Tears down on exit.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-cli-gate.XXXXXX)
trap 'rm -rf "$tmp"; [[ -n "${hub_pid:-}" ]] && kill "$hub_pid" 2>/dev/null || true' EXIT

hub_data="$tmp/hub-data"
cli_config="$tmp/cli/config.toml"
cli_fif="$tmp/cli/identity.fif"
cli_grant="$tmp/cli/vault.macaroon"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17944)
target="http://127.0.0.1:${port}"

echo "==> Building binaries"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)
(cd nakli-cli && go build -o "$tmp/nakli-cli" ./cmd/nakli-cli)

echo "==> Initializing + starting Hub"
"$tmp/nakli-hub" init --data-dir "$hub_data" >"$tmp/hub-init.log"
"$tmp/nakli-hub" serve \
  --config "$hub_data/config.json" \
  --listen "127.0.0.1:${port}" \
  --peer-url "http://127.0.0.1:1/unreachable" \
  >"$tmp/hub.log" 2>&1 &
hub_pid=$!

for _ in $(seq 1 50); do
  if curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

echo "==> nakli-cli init (non-interactive)"
printf 'pass-cli-gate-1\n' | "$tmp/nakli-cli" \
  --config "$cli_config" \
  --passphrase-stdin \
  init --non-interactive \
       --display-name "M4 Gate" \
       --fif "$cli_fif" \
       --hub-url "$target" \
       --hub-data-dir "$hub_data"

echo "==> nakli-cli grant mint (vault read+write)"
"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01J0CLIGATERECIPIENT00000001" \
  --primitive vault --namespace "*" --operations read,write \
  --output "$cli_grant" >/dev/null

echo "==> nakli-cli vault append"
echo '{"item":"milk","qty":2}' | "$tmp/nakli-cli" --config "$cli_config" --json \
  vault append --namespace list --stream-id "01J0CLIGATESTREAM0000000001" \
  --kind "list:item-added" --grant "$cli_grant" > "$tmp/append.json"
cat "$tmp/append.json"
event_id=$(python3 -c "import json,sys; print(json.load(open('$tmp/append.json'))['data']['event_id'])")
if [[ -z "$event_id" ]]; then
  echo "FAIL: vault append did not return event_id"
  exit 1
fi

echo "==> nakli-cli vault read"
"$tmp/nakli-cli" --config "$cli_config" vault read \
  --namespace list --stream-id "01J0CLIGATESTREAM0000000001" \
  --grant "$cli_grant" > "$tmp/read.txt"
cat "$tmp/read.txt"
grep -q "$event_id" "$tmp/read.txt" || { echo "FAIL: vault read did not echo event_id $event_id"; exit 1; }
grep -q 'milk' "$tmp/read.txt" || { echo "FAIL: vault read did not include decoded payload"; exit 1; }

echo "==> nakli-cli conformance"
"$tmp/nakli-cli" --config "$cli_config" conformance > "$tmp/conf.txt"
tail -2 "$tmp/conf.txt"
grep -q "32/32 passing" "$tmp/conf.txt" || { echo "FAIL: conformance did not report 32/32"; exit 1; }

echo "==> cli-gate done — M4 gate green"
