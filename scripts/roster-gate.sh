#!/usr/bin/env bash
# roster-gate.sh — M8 demo-mode gate: build the SDK bundle (so the same
# static server can host the roster page), copy roster.html into the
# Playwright fixtures dir, run the demo-mode Playwright suites (s1, s3,
# s4) across Chromium + Firefox + WebKit.
#
# session 1: mock list renders + CRUD + inline edit
# session 3: multi-list switcher + reorder + qty edit + fractional indexing
# session 4: operator drawer + .naklilist export + a11y essentials
#
# Session 2 needs a real Hub — run `scripts/roster-fabric-gate.sh`.
#
# roster.html lives in the sibling NakliTechie/roster repo. Default path
# assumes the post-reorg layout (~/Code/naklios-universe/{private-mesh-universe/private-mesh,roster}).
# Override with ROSTER_REPO=/absolute/path/to/roster for other layouts.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

roster_repo="${ROSTER_REPO:-$repo_root/../../roster}"
if [[ ! -f "$roster_repo/roster.html" ]]; then
  alt="$repo_root/../roster"
  if [[ -f "$alt/roster.html" ]]; then
    roster_repo="$alt"
  else
    echo "FAIL: roster.html not found. Set ROSTER_REPO to the roster repo path." >&2
    echo "  Tried: $roster_repo/roster.html" >&2
    echo "  Tried: $alt/roster.html" >&2
    exit 1
  fi
fi

tmp=$(mktemp -d -t roster-gate.XXXXXX)
trap '
  [[ -n "${sdk_pid:-}" ]] && kill "$sdk_pid" 2>/dev/null || true
  rm -rf "$tmp"
  rm -f fabric-sdk-js/browser-test/pages/roster.html
' EXIT

sdk_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 5183)
sdk_url="http://127.0.0.1:${sdk_port}"

echo "==> Building fabric-sdk-js bundle (needed by the existing test server)"
(cd fabric-sdk-js && node scripts/build.mjs > "$tmp/build.log")

echo "==> Staging roster.html (from $roster_repo) under fabric-sdk-js/browser-test/pages/"
cp "$roster_repo/roster.html" fabric-sdk-js/browser-test/pages/roster.html

echo "==> Starting static server on $sdk_url"
(cd fabric-sdk-js && node scripts/serve-test.mjs "$sdk_port" > "$tmp/sdk.log" 2>&1) &
sdk_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "${sdk_url}/pages/roster.html" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> Running Playwright (roster-s1 + s3 + s4 across all installed projects)"
project_flag=""
if [[ -n "${PLAYWRIGHT_PROJECT:-}" ]]; then
  project_flag="--project=${PLAYWRIGHT_PROJECT}"
fi
(cd fabric-sdk-js && SDK_TEST_BASE_URL="$sdk_url" \
  pnpm exec playwright test ${project_flag} \
    browser-test/roster-s1.spec.js \
    browser-test/roster-s3.spec.js \
    browser-test/roster-s4.spec.js)

echo "==> roster-gate done — M8 demo-mode (s1/s3/s4) green"
