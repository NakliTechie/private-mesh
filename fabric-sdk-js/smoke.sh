#!/usr/bin/env bash
# Smoke test for fabric-sdk-js. As of M1 this runs the unit-test suite.
# Auto-installs node_modules/ on first run so CI doesn't need a separate step.
set -euo pipefail
cd "$(dirname "$0")"
if [[ ! -d node_modules ]]; then
  pnpm install --silent > /dev/null
fi
PASS_COUNT=$(pnpm test 2>&1 | awk '/^ℹ pass / {print $3}')
echo "OK: fabric-sdk-js (${PASS_COUNT:-?} tests passing)"
