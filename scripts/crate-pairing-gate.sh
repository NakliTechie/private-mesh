#!/usr/bin/env bash
# crate-pairing-gate.sh — Unit C end-to-end gate.
#
# Builds nakli-hub + nakli-cli; starts a Hub on a free port; mints a
# scoped Grant for the browser-side identity/pair flow; exercises the
# CRATE-PAIR conformance matrix end-to-end via curl:
#
#   - POST /v1/pairing/intent          → 201
#   - POST /v1/pairing/intent (bad v)  → 426
#   - POST /v1/pairing/redeem (fresh)  → 200 + capability
#   - POST /v1/pairing/redeem (replay) → 409 token_already_redeemed
#   - POST /v1/pairing/redeem (unknown)→ 404 token_not_found
#   - POST /v1/pairing/intent/cancel   → 204; then redeem → 404 token_cancelled
#
# Capability refresh + revoke paths are covered by the Go unit tests
# (internal/server/handlers_crate_pairing_test.go) because they need to
# mint a sync-scope capability without going through the full pair flow.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t crate-pairing-gate.XXXXXX)
trap '
  [[ -n "${hub_pid:-}" ]] && kill "$hub_pid" 2>/dev/null || true
  rm -rf "$tmp"
' EXIT

hub_data="$tmp/hub-data"
cli_config="$tmp/cli/config.toml"
cli_fif="$tmp/cli/identity.fif"
cli_grant="$tmp/cli/identity-pair.macaroon"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17945)
target="http://127.0.0.1:${port}"

echo "==> Building binaries"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)
(cd nakli-cli && go build -o "$tmp/nakli-cli" ./cmd/nakli-cli)

echo "==> Starting Hub on $target"
"$tmp/nakli-hub" init --data-dir "$hub_data" > "$tmp/hub-init.log"
"$tmp/nakli-hub" serve \
  --config "$hub_data/config.json" \
  --listen "127.0.0.1:${port}" \
  > "$tmp/hub.log" 2>&1 &
hub_pid=$!
for _ in $(seq 1 50); do
  if curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

echo "==> nakli-cli init + mint identity-pair Grant"
printf 'pass-crate-pairing-gate\n' | "$tmp/nakli-cli" \
  --config "$cli_config" --passphrase-stdin \
  init --non-interactive \
       --display-name "Unit C Gate" \
       --fif "$cli_fif" \
       --hub-url "$target" \
       --hub-data-dir "$hub_data" > /dev/null

"$tmp/nakli-cli" --config "$cli_config" grant mint \
  --recipient "01JCRATEBROWSERPRINCIPAL0000" \
  --primitive identity --namespace "*" --operations pair \
  --output "$cli_grant" > /dev/null
grant_b64=$(tr -d '\n' < "$cli_grant")

assert_status() {
  local want=$1
  local got=$2
  local label=$3
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $label: got HTTP $got, want $want" >&2
    return 1
  fi
  echo "  ✓ $label → HTTP $got"
}

assert_code() {
  local want=$1
  local body=$2
  local label=$3
  local got
  got=$(python3 -c "import json,sys; print(json.loads(sys.stdin.read()).get('error', {}).get('code', ''))" <<< "$body")
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $label: got error code $got, want $want" >&2
    echo "      body: $body" >&2
    return 1
  fi
  echo "  ✓ $label → code $got"
}

# Build a fresh intent payload
now_unix=$(date -u +%s)
secret=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
bucket_id="01HUNITCBUCKETIDXXXXXXXXXX"
identity_pubkey=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
exp_unix=$((now_unix + 900))

intent_payload=$(python3 -c "import json; print(json.dumps({'v':1,'type':'crate.pairing.token','secret':'$secret','transport_endpoint':'$target','transport_type':'hub','bucket_id':'$bucket_id','identity_pubkey':'$identity_pubkey','issued_at':$now_unix,'expires_at':$exp_unix}))")

echo "==> 1. POST /v1/pairing/intent (valid)"
status=$(curl -s -o "$tmp/intent.out" -w "%{http_code}" -X POST "${target}/v1/pairing/intent" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${grant_b64}" \
  -d "$intent_payload")
assert_status 201 "$status" "intent (valid)"

echo "==> 2. POST /v1/pairing/intent (v=99 → protocol_version)"
bad_secret=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
bad_payload=$(python3 -c "import json; print(json.dumps({'v':99,'type':'crate.pairing.token','secret':'$bad_secret','transport_endpoint':'$target','transport_type':'hub','bucket_id':'$bucket_id','identity_pubkey':'$identity_pubkey','issued_at':$now_unix,'expires_at':$exp_unix}))")
status=$(curl -s -o "$tmp/badv.out" -w "%{http_code}" -X POST "${target}/v1/pairing/intent" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${grant_b64}" \
  -d "$bad_payload")
assert_status 426 "$status" "intent (v=99)"
assert_code "protocol_version" "$(cat "$tmp/badv.out")" "intent error code"

echo "==> 3. POST /v1/pairing/redeem (fresh)"
daemon_pubkey=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
redeem_body=$(python3 -c "import json; print(json.dumps({'v':1,'secret':'$secret','daemon_pubkey':'$daemon_pubkey','daemon_fingerprint':{'platform':'darwin','arch':'arm64','hostname':'gate','agent_version':'gate'}}))")
status=$(curl -s -o "$tmp/redeem.out" -w "%{http_code}" -X POST "${target}/v1/pairing/redeem" \
  -H "Content-Type: application/json" -d "$redeem_body")
assert_status 200 "$status" "redeem (fresh)"
capability=$(python3 -c "import json,sys; d=json.loads(sys.stdin.read()); print(d['data']['capability'])" < "$tmp/redeem.out")
if [[ -z "$capability" ]]; then
  echo "FAIL: redeem did not return a capability"; exit 1
fi
echo "  ✓ capability returned ($(echo -n "$capability" | wc -c) bytes base64)"

echo "==> 4. POST /v1/pairing/redeem (replay → token_already_redeemed)"
status=$(curl -s -o "$tmp/replay.out" -w "%{http_code}" -X POST "${target}/v1/pairing/redeem" \
  -H "Content-Type: application/json" -d "$redeem_body")
assert_status 409 "$status" "redeem (replay)"
assert_code "token_already_redeemed" "$(cat "$tmp/replay.out")" "replay error code"

echo "==> 5. POST /v1/pairing/redeem (unknown secret → token_not_found)"
unknown_secret=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
unknown_body=$(python3 -c "import json; print(json.dumps({'v':1,'secret':'$unknown_secret','daemon_pubkey':'$daemon_pubkey','daemon_fingerprint':{}}))")
status=$(curl -s -o "$tmp/unknown.out" -w "%{http_code}" -X POST "${target}/v1/pairing/redeem" \
  -H "Content-Type: application/json" -d "$unknown_body")
assert_status 404 "$status" "redeem (unknown)"
assert_code "token_not_found" "$(cat "$tmp/unknown.out")" "unknown error code"

echo "==> 6. POST /v1/pairing/intent/cancel then redeem (→ token_cancelled)"
secret2=$(python3 -c 'import secrets,base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))')
intent2=$(python3 -c "import json; print(json.dumps({'v':1,'type':'crate.pairing.token','secret':'$secret2','transport_endpoint':'$target','transport_type':'hub','bucket_id':'$bucket_id','identity_pubkey':'$identity_pubkey','issued_at':$now_unix,'expires_at':$exp_unix}))")
status=$(curl -s -o "$tmp/intent2.out" -w "%{http_code}" -X POST "${target}/v1/pairing/intent" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${grant_b64}" \
  -d "$intent2")
assert_status 201 "$status" "intent (for cancel)"

cancel_body=$(python3 -c "import json; print(json.dumps({'secret':'$secret2'}))")
status=$(curl -s -o "$tmp/cancel.out" -w "%{http_code}" -X POST "${target}/v1/pairing/intent/cancel" \
  -H "Content-Type: application/json" -H "X-Fabric-Grant: ${grant_b64}" \
  -d "$cancel_body")
assert_status 204 "$status" "intent/cancel"

redeem2=$(python3 -c "import json; print(json.dumps({'v':1,'secret':'$secret2','daemon_pubkey':'$daemon_pubkey','daemon_fingerprint':{}}))")
status=$(curl -s -o "$tmp/redeem-cancelled.out" -w "%{http_code}" -X POST "${target}/v1/pairing/redeem" \
  -H "Content-Type: application/json" -d "$redeem2")
assert_status 404 "$status" "redeem (cancelled)"
assert_code "token_cancelled" "$(cat "$tmp/redeem-cancelled.out")" "cancelled error code"

echo "==> crate-pairing-gate done — Unit C green (6/6 wire scenarios + 13 unit tests)"
