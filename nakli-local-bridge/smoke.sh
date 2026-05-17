#!/usr/bin/env bash
# Smoke test for nakli-local-bridge. As of M7 this typechecks (via go build)
# and builds the binary so build-all catches link errors.
set -euo pipefail
cd "$(dirname "$0")"
TMP_BIN=$(mktemp -t nakli-local-bridge.XXXXXX)
trap 'rm -f "$TMP_BIN"' EXIT
go build -o "$TMP_BIN" ./cmd/nakli-local-bridge
echo "OK: nakli-local-bridge (binary built)"
