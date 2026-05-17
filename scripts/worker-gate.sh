#!/usr/bin/env bash
# worker-gate.sh — M6 gate: spin up nakli-cf-worker locally via `wrangler dev`
# and run the 32-test conformance suite against it.
#
# We do not deploy to Cloudflare here — that requires an account, billing, and
# real R2/KV namespaces. `wrangler dev` simulates both locally via Miniflare,
# which is the same path Cloudflare itself documents for CI.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-worker-gate.XXXXXX)
trap '
  [[ -n "${wrangler_pid:-}" ]] && kill "$wrangler_pid" 2>/dev/null || true
  rm -rf "$tmp"
' EXIT

hub_data="$tmp/hub-data"

worker_port=18934
target="http://127.0.0.1:${worker_port}"

echo "==> Building nakli-hub (for conformance runner)"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)

echo "==> Generating Hub identity for the Worker to use"
"$tmp/nakli-hub" init --data-dir "$hub_data" > "$tmp/hub-init.log"

# Extract pieces the Worker needs. hub-identity.json's *_key fields are
# already base64 strings — pass through verbatim.
hub_id=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['hub_id'])")
hub_public=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['public_key'])")
hub_private=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['private_key'])")
mac_root=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['macaroon_root_key'])")

cat > nakli-cf-worker/.dev.vars <<EOF
HUB_ID=${hub_id}
HUB_PUBLIC_KEY=${hub_public}
HUB_PRIVATE_KEY=${hub_private}
MACAROON_ROOT_KEY=${mac_root}
CONFORMANCE_MODE=true
PEER_URL=http://127.0.0.1:1/unreachable
EOF

echo "==> Starting wrangler dev on $target"
(cd nakli-cf-worker && pnpm exec wrangler dev --port "$worker_port" --ip 127.0.0.1 --local --no-show-interactive-dev-session > "$tmp/wrangler.log" 2>&1) &
wrangler_pid=$!

# Wait for the Worker to come up.
for _ in $(seq 1 200); do
  if curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
if ! curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then
  echo "wrangler dev didn't come up in time:"
  tail -50 "$tmp/wrangler.log"
  exit 1
fi
# Kick the conformance-seed endpoint (idempotent).
curl -fsS -X POST "${target}/fabric/v1/_conformance/setup" > /dev/null || true

echo "==> Running conformance against the Worker"
"$tmp/nakli-hub" conformance --target "$target" --data-dir "$hub_data" > "$tmp/conf.txt"
tail -2 "$tmp/conf.txt"
grep -q "32/32 passing" "$tmp/conf.txt" || { echo "FAIL: conformance did not report 32/32"; cat "$tmp/conf.txt"; exit 1; }

echo "==> worker-gate done — M6 gate green"
