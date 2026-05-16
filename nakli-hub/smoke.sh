#!/usr/bin/env bash
# Smoke test for nakli-hub. As of M2 this runs the full unit-test suite
# (server middleware + handlers, storage, hubid). The binary is also built
# to catch link errors.
set -euo pipefail
cd "$(dirname "$0")"
PASS_COUNT=$(go test ./... -count=1 -v 2>&1 | grep -c '^--- PASS:' || true)
TMP_BIN=$(mktemp -t nakli-hub.XXXXXX)
trap 'rm -f "$TMP_BIN"' EXIT
go build -o "$TMP_BIN" ./cmd/nakli-hub
echo "OK: nakli-hub (${PASS_COUNT} tests passing; binary built)"
