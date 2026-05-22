#!/usr/bin/env bash
# Smoke test for fabric-sdk-js. As of M1 this runs the unit-test suite.
# Auto-installs node_modules/ on first run so CI doesn't need a separate step.
set -euo pipefail
cd "$(dirname "$0")"
if [[ ! -d node_modules ]]; then
  pnpm install --silent > /dev/null
fi
# Capture full output so we can both extract PASS_COUNT and print the raw
# log on failure. The earlier `pnpm test 2>&1 | awk` form swallowed all
# test output, leaving build-all failures invisible in CI.
out=$(mktemp)
trap 'rm -f "$out"' EXIT
if ! pnpm test > "$out" 2>&1; then
  echo "FAIL: fabric-sdk-js — pnpm test exited non-zero"
  cat "$out"
  exit 1
fi
PASS_COUNT=$(awk '/^ℹ pass / {print $3}' "$out")
echo "OK: fabric-sdk-js (${PASS_COUNT:-?} tests passing)"
