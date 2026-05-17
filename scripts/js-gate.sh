#!/usr/bin/env bash
# js-gate.sh — exercise the M5 gate: a browser tool consuming fabric-sdk-js
# appends to and reads from a real Hub. Builds nakli-hub + nakli-cli + the
# JS SDK bundle, starts the Hub, mints a wildcard Grant, generates a 32-byte
# root seed, then runs Playwright against http://127.0.0.1:<sdk-port>/sandbox.html.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-js-gate.XXXXXX)
trap '
  [[ -n "${hub_pid:-}" ]] && kill "$hub_pid" 2>/dev/null || true
  [[ -n "${sdk_pid:-}" ]] && kill "$sdk_pid" 2>/dev/null || true
  rm -rf "$tmp"
' EXIT

hub_data="$tmp/hub-data"
cli_config="$tmp/cli/config.toml"
cli_grant="$tmp/cli/sdk.macaroon"
gate_config="$repo_root/fabric-sdk-js/browser-test/gate-config.json"

# Free ports for Hub + the SDK's static server.
hub_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 18742)
sdk_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 5172)
hub_url="http://127.0.0.1:${hub_port}"
sdk_url="http://127.0.0.1:${sdk_port}"

echo "==> Building binaries"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)
(cd nakli-cli && go build -o "$tmp/nakli-cli" ./cmd/nakli-cli)

echo "==> Building fabric-sdk-js bundle"
(cd fabric-sdk-js && node scripts/build.mjs > "$tmp/build.log")
tail -3 "$tmp/build.log"

echo "==> Initializing + starting Hub on $hub_url"
"$tmp/nakli-hub" init --data-dir "$hub_data" > "$tmp/hub-init.log"
"$tmp/nakli-hub" serve \
  --config "$hub_data/config.json" \
  --listen "127.0.0.1:${hub_port}" \
  > "$tmp/hub.log" 2>&1 &
hub_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "${hub_url}/fabric/v1/health" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> nakli-cli init + grant mint (wildcard)"
printf 'pass-js-gate\n' | "$tmp/nakli-cli" \
  --config "$cli_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "JS Gate" \
       --fif "$tmp/cli/identity.fif" \
       --hub-url "$hub_url" \
       --hub-data-dir "$hub_data" > /dev/null

"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01J0SDKBROWSERPRINCIPAL0000" \
  --primitive vault --namespace "*" --operations read,write \
  --output "$cli_grant" > /dev/null

grant_b64=$(tr -d '\n' < "$cli_grant")
root_seed_hex=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
stream_id=$(python3 -c 'import secrets; print(secrets.token_hex(13).upper())')

cat > "$gate_config" <<EOF
{
  "hubUrl": "${hub_url}",
  "grant": "${grant_b64}",
  "rootSeedHex": "${root_seed_hex}",
  "streamId": "${stream_id}"
}
EOF

echo "==> Starting SDK static server on $sdk_url"
(cd fabric-sdk-js && node scripts/serve-test.mjs "$sdk_port" > "$tmp/sdk.log" 2>&1) &
sdk_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "${sdk_url}/sandbox.html" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> Running Playwright"
project_flag=""
if [[ -n "${PLAYWRIGHT_PROJECT:-}" ]]; then
  project_flag="--project=${PLAYWRIGHT_PROJECT}"
fi
(cd fabric-sdk-js && SDK_TEST_BASE_URL="$sdk_url" \
  SDK_GATE_CONFIG="$gate_config" \
  pnpm exec playwright test ${project_flag} browser-test/m5-gate.spec.js)

echo "==> js-gate done — M5 browser gate green"
