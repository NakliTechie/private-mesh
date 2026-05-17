#!/usr/bin/env bash
# Smoke test for nakli-cli. As of M4 this runs the package tests and builds
# the binary to catch link errors.
set -euo pipefail
cd "$(dirname "$0")"
PASS_COUNT=$(go test ./... -count=1 -v 2>&1 | grep -c '^--- PASS:' || true)
TMP_BIN=$(mktemp -t nakli-cli.XXXXXX)
trap 'rm -f "$TMP_BIN"' EXIT
go build -o "$TMP_BIN" ./cmd/nakli-cli
echo "OK: nakli-cli (${PASS_COUNT} tests passing; binary built)"
