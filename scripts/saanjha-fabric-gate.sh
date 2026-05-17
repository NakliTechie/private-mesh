#!/usr/bin/env bash
# saanjha-fabric-gate.sh — M8 session 2 gate: saanjha.html, fabric-sdk-js,
# and a real Hub round-trip. Builds nakli-hub + nakli-cli + the JS SDK
# bundle, stands the Hub up on a free port, mints a wildcard Grant,
# stages saanjha.html alongside the SDK bundle, and runs the session-2
# Playwright suite (which injects window.__GATE so saanjha boots into
# Fabric mode and writes/reads to the live Hub).
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t saanjha-fabric-gate.XXXXXX)
trap '
  [[ -n "${hub_pid:-}" ]] && kill "$hub_pid" 2>/dev/null || true
  [[ -n "${sdk_pid:-}" ]] && kill "$sdk_pid" 2>/dev/null || true
  rm -rf "$tmp"
  rm -f fabric-sdk-js/browser-test/pages/saanjha.html
  rm -f fabric-sdk-js/browser-test/pages/fabric-sdk.js
  rm -f fabric-sdk-js/browser-test/pages/fabric-sdk.js.map
' EXIT

hub_data="$tmp/hub-data"
cli_config="$tmp/cli/config.toml"
cli_grant="$tmp/cli/sdk.macaroon"
gate_config="$repo_root/fabric-sdk-js/browser-test/saanjha-gate-config.json"

hub_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 18743)
sdk_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 5183)
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
printf 'pass-saanjha-gate\n' | "$tmp/nakli-cli" \
  --config "$cli_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "Saanjha Gate" \
       --fif "$tmp/cli/identity.fif" \
       --hub-url "$hub_url" \
       --hub-data-dir "$hub_data" > /dev/null

"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01J0SDKBROWSERPRINCIPAL0000" \
  --primitive vault --namespace "*" --operations read,write \
  --output "$cli_grant" > /dev/null

grant_b64=$(tr -d '\n' < "$cli_grant")
root_seed_hex=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
# Use a Crockford-base32 ULID-shaped string (no I/L/O/U) so the Vault accepts it.
stream_id=$(python3 -c 'import secrets,string; a="0123456789ABCDEFGHJKMNPQRSTVWXYZ"; print("".join(secrets.choice(a) for _ in range(26)))')

cat > "$gate_config" <<EOF
{
  "hubUrl": "${hub_url}",
  "grant": "${grant_b64}",
  "rootSeedHex": "${root_seed_hex}",
  "streamId": "${stream_id}",
  "namespace": "saanjha",
  "principalId": "01J0SDKBROWSERPRINCIPAL0000",
  "listName": "Groceries"
}
EOF

echo "==> Staging saanjha.html + SDK bundle under fabric-sdk-js/browser-test/pages/"
cp saanjha/saanjha.html fabric-sdk-js/browser-test/pages/saanjha.html
cp fabric-sdk-js/dist/fabric-sdk.js     fabric-sdk-js/browser-test/pages/fabric-sdk.js
cp fabric-sdk-js/dist/fabric-sdk.js.map fabric-sdk-js/browser-test/pages/fabric-sdk.js.map

echo "==> Starting static server on $sdk_url"
(cd fabric-sdk-js && node scripts/serve-test.mjs "$sdk_port" > "$tmp/sdk.log" 2>&1) &
sdk_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "${sdk_url}/pages/saanjha.html" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> Running Playwright (saanjha-s2 across all installed projects)"
project_flag=""
if [[ -n "${PLAYWRIGHT_PROJECT:-}" ]]; then
  project_flag="--project=${PLAYWRIGHT_PROJECT}"
fi
(cd fabric-sdk-js && SDK_TEST_BASE_URL="$sdk_url" \
  SAANJHA_GATE_CONFIG="$gate_config" \
  pnpm exec playwright test ${project_flag} browser-test/saanjha-s2.spec.js)

echo "==> saanjha-fabric-gate done — M8 session 2 green"
