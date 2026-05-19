#!/usr/bin/env bash
# crate-bucket-worker-gate.sh — cf-worker bucket-proxy parity gate (M3 piece 2).
#
# Spins up nakli-cf-worker locally via `wrangler dev` (Miniflare) and runs
# the same 8 wire scenarios as scripts/crate-bucket-gate.sh against the
# Worker. The handlers + sig-v4 + cred-sealing live in
# nakli-cf-worker/src/crate-bucket.ts.
#
# Scenarios:
#   1. POST /v1/crate/bucket/register (valid R2)           → 201 {bucket_id}
#   2. POST /v1/crate/bucket/register (missing fields)     → 400 bad_request
#   3. POST /v1/crate/bucket/register (unknown provider)   → 400 bad_request
#   4. GET  /v1/crate/bucket/{bucket_id} (good sync scope) → 200 + metadata
#   5. GET  /v1/crate/bucket/{unknown}                      → 404 OR 403
#   6. GET  /v1/crate/object/{bucket_id}/x (wrong scope)   → 403
#   7. PUT  /v1/crate/object/{bucket_id}/x (read-only)     → 403
#   8. GET  /v1/crate/object/{bucket_id}/x (no Grant)      → 401
#
# Proxy round-trip → real upstream is NOT exercised here (Worker → external
# R2 from a wrangler-dev sandbox is flaky in CI); the byte-identical sig-v4
# implementation is verified by the Hub-side Go tests + the AWS reference
# vector.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-crate-bucket-worker.XXXXXX)
trap '
  [[ -n "${wrangler_pid:-}" ]] && kill "$wrangler_pid" 2>/dev/null || true
  rm -rf "$tmp"
  rm -f nakli-cf-worker/.dev.vars
' EXIT

hub_data="$tmp/hub-data"
cli_config="$tmp/cli/config.toml"
cli_fif="$tmp/cli/identity.fif"
register_grant="$tmp/cli/register.macaroon"
sync_grant="$tmp/cli/sync.macaroon"
wrong_grant="$tmp/cli/sync-wrong.macaroon"
ro_grant="$tmp/cli/sync-readonly.macaroon"

worker_port=18936
target="http://127.0.0.1:${worker_port}"

echo "==> Building nakli-hub + nakli-cli"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)
(cd nakli-cli && go build -o "$tmp/nakli-cli" ./cmd/nakli-cli)

echo "==> Generating Hub identity for the Worker"
"$tmp/nakli-hub" init --data-dir "$hub_data" > "$tmp/hub-init.log"
hub_id=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['hub_id'])")
hub_public=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['public_key'])")
hub_private=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['private_key'])")
mac_root=$(python3 -c "import json; print(json.load(open('$hub_data/hub-identity.json'))['macaroon_root_key'])")

cat > nakli-cf-worker/.dev.vars <<EOF
HUB_ID=${hub_id}
HUB_PUBLIC_KEY=${hub_public}
HUB_PRIVATE_KEY=${hub_private}
MACAROON_ROOT_KEY=${mac_root}
PEER_URL=http://127.0.0.1:1/unreachable
EOF

echo "==> Starting wrangler dev on $target"
(cd nakli-cf-worker && pnpm exec wrangler dev --port "$worker_port" --ip 127.0.0.1 --local --no-show-interactive-dev-session > "$tmp/wrangler.log" 2>&1) &
wrangler_pid=$!

for _ in $(seq 1 200); do
  if curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then break; fi
  sleep 0.2
done
if ! curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then
  echo "wrangler dev didn't come up:"
  tail -50 "$tmp/wrangler.log"
  exit 1
fi

echo "==> nakli-cli init + mint identity-pair Grant"
printf 'pass-cbw-gate\n' | "$tmp/nakli-cli" \
  --config "$cli_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "crate-bucket cf-worker Gate" \
       --fif "$cli_fif" \
       --hub-url "$target" \
       --hub-data-dir "$hub_data" > /dev/null

"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01JCRATEBROWSERPRINCIPAL0000" \
  --primitive identity --namespace "*" --operations pair \
  --output "$register_grant" > /dev/null
register_grant_b64=$(tr -d '\n' < "$register_grant")

assert_status() {
  local want=$1 got=$2 label=$3
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $label: got HTTP $got, want $want" >&2
    exit 1
  fi
  echo "  ✓ $label → HTTP $got"
}

assert_code() {
  local want=$1 body=$2 label=$3
  local got
  got=$(python3 -c "import json,sys; print(json.loads(sys.stdin.read()).get('error', {}).get('code', ''))" <<< "$body")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $label: got error code $got, want $want" >&2
    echo "      body: $body" >&2
    exit 1
  fi
  echo "  ✓ $label → code $got"
}

echo "==> 1. POST /v1/crate/bucket/register (valid R2)"
register_body=$(python3 -c '
import json
print(json.dumps({
  "provider": "r2",
  "account_id": "62231b040ed00c96cdcf3a4541eab958",
  "region": "auto",
  "bucket_name": "crate-bucket-worker-gate",
  "access_key": "ak-test",
  "secret_key": "sk-test-aaaaaaaaaaaaaaaaaaaaaa",
}))')
status=$(curl -s -o "$tmp/register.out" -w "%{http_code}" -X POST "${target}/v1/crate/bucket/register" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${register_grant_b64}" \
  -d "$register_body")
assert_status 201 "$status" "register (valid R2)"
bucket_id=$(python3 -c "import json,sys; d=json.loads(sys.stdin.read()); print(d['data']['bucket_id'])" < "$tmp/register.out")
if [[ -z "$bucket_id" ]]; then
  echo "FAIL: no bucket_id returned"; exit 1
fi
echo "  ✓ bucket_id=$bucket_id"

echo "==> 2. POST /v1/crate/bucket/register (missing access_key → 400)"
bad_body=$(python3 -c '
import json
print(json.dumps({
  "provider": "r2", "account_id": "62231b040ed00c96cdcf3a4541eab958",
  "region": "auto", "bucket_name": "x", "secret_key": "sk",
}))')
status=$(curl -s -o "$tmp/badreg.out" -w "%{http_code}" -X POST "${target}/v1/crate/bucket/register" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${register_grant_b64}" \
  -d "$bad_body")
assert_status 400 "$status" "register (missing fields)"
assert_code "bad_request" "$(cat "$tmp/badreg.out")" "register missing-fields code"

echo "==> 3. POST /v1/crate/bucket/register (unknown provider → 400)"
unknown_body='{"provider":"wasabi","region":"us-east-1","bucket_name":"x","access_key":"a","secret_key":"b"}'
status=$(curl -s -o "$tmp/unkprov.out" -w "%{http_code}" -X POST "${target}/v1/crate/bucket/register" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${register_grant_b64}" \
  -d "$unknown_body")
assert_status 400 "$status" "register (unknown provider)"

echo "==> 4. GET /v1/crate/bucket/{bucket_id} (sync scope)"
"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01JCRATEBROWSERPRINCIPAL0000" \
  --primitive sync --namespace "$bucket_id" --operations read,write \
  --output "$sync_grant" > /dev/null
sync_grant_b64=$(tr -d '\n' < "$sync_grant")
status=$(curl -s -o "$tmp/meta.out" -w "%{http_code}" \
  -H "X-Fabric-Grant: ${sync_grant_b64}" \
  "${target}/v1/crate/bucket/${bucket_id}")
assert_status 200 "$status" "metadata (good scope)"
if ! grep -q '"provider":"r2"' "$tmp/meta.out"; then
  echo "FAIL: metadata missing provider=r2: $(cat "$tmp/meta.out")"
  exit 1
fi
echo "  ✓ metadata includes provider=r2"

echo "==> 5. GET /v1/crate/bucket/{unknown_id} → 404 or 403"
status=$(curl -s -o "$tmp/unk.out" -w "%{http_code}" \
  -H "X-Fabric-Grant: ${sync_grant_b64}" \
  "${target}/v1/crate/bucket/bk_does_not_exist")
if [[ "$status" != "404" && "$status" != "403" ]]; then
  echo "FAIL: unknown bucket: got $status, want 404 or 403"; exit 1
fi
echo "  ✓ unknown bucket → HTTP $status"

echo "==> 6. GET /v1/crate/object/{bucket_id}/x with wrong-scope Grant → 403"
"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01JCRATEBROWSERPRINCIPAL0000" \
  --primitive sync --namespace "bk_other_bucket" --operations read,write \
  --output "$wrong_grant" > /dev/null
wrong_grant_b64=$(tr -d '\n' < "$wrong_grant")
status=$(curl -s -o "$tmp/wrongscope.out" -w "%{http_code}" \
  -H "X-Fabric-Grant: ${wrong_grant_b64}" \
  "${target}/v1/crate/object/${bucket_id}/some-file.txt")
assert_status 403 "$status" "object GET (wrong namespace)"

echo "==> 7. PUT /v1/crate/object/{bucket_id}/x with read-only sync Grant → 403"
"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01JCRATEBROWSERPRINCIPAL0000" \
  --primitive sync --namespace "$bucket_id" --operations read \
  --output "$ro_grant" > /dev/null
ro_grant_b64=$(tr -d '\n' < "$ro_grant")
status=$(curl -s -o "$tmp/ro.out" -w "%{http_code}" -X PUT \
  -H "X-Fabric-Grant: ${ro_grant_b64}" \
  --data-binary "nope" \
  "${target}/v1/crate/object/${bucket_id}/x.txt")
assert_status 403 "$status" "object PUT (read-only)"

echo "==> 8. GET /v1/crate/object/{bucket_id}/x without any Grant → 401"
status=$(curl -s -o "$tmp/noauth.out" -w "%{http_code}" \
  "${target}/v1/crate/object/${bucket_id}/x.txt")
assert_status 401 "$status" "object GET (no Grant)"

echo "==> crate-bucket-worker-gate done — 8/8 cf-worker scenarios green"
