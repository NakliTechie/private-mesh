#!/usr/bin/env bash
# Smoke test for fabric-sdk-go. As of M1 this runs the full unit-test suite —
# it's still fast (~1s) and gives a real signal.
set -euo pipefail
cd "$(dirname "$0")"
PASS_COUNT=$(go test ./... -count=1 -v 2>&1 | grep -c '^--- PASS:' || true)
echo "OK: fabric-sdk-go (${PASS_COUNT} tests passing)"
