#!/usr/bin/env bash
# saanjha-gate.sh — M8 session-1 gate: build the SDK bundle (so the same
# static server can host the saanjha page), copy saanjha.html into the
# Playwright fixtures dir, run the session-1 Playwright suite. Three
# browser projects (Chromium + Firefox + WebKit) all assert: mock list
# renders, add + check + filter all work, and inline edit commits.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t saanjha-gate.XXXXXX)
trap '
  [[ -n "${sdk_pid:-}" ]] && kill "$sdk_pid" 2>/dev/null || true
  rm -rf "$tmp"
  rm -f fabric-sdk-js/browser-test/pages/saanjha.html
' EXIT

sdk_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 5183)
sdk_url="http://127.0.0.1:${sdk_port}"

echo "==> Building fabric-sdk-js bundle (needed by the existing test server)"
(cd fabric-sdk-js && node scripts/build.mjs > "$tmp/build.log")

echo "==> Staging saanjha.html under fabric-sdk-js/browser-test/pages/"
cp saanjha/saanjha.html fabric-sdk-js/browser-test/pages/saanjha.html

echo "==> Starting static server on $sdk_url"
(cd fabric-sdk-js && node scripts/serve-test.mjs "$sdk_port" > "$tmp/sdk.log" 2>&1) &
sdk_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "${sdk_url}/pages/saanjha.html" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> Running Playwright (saanjha-s1 across all installed projects)"
project_flag=""
if [[ -n "${PLAYWRIGHT_PROJECT:-}" ]]; then
  project_flag="--project=${PLAYWRIGHT_PROJECT}"
fi
(cd fabric-sdk-js && SDK_TEST_BASE_URL="$sdk_url" \
  pnpm exec playwright test ${project_flag} browser-test/saanjha-s1.spec.js)

echo "==> saanjha-gate done — M8 session 1 green"
