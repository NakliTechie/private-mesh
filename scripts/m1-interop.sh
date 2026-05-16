#!/usr/bin/env bash
# m1-interop.sh — cross-SDK gate for M1.
# Runs Go and JS interop CLIs in sequence: each side generates fixtures from
# shared vectors, and the other side verifies. Fails fast on any mismatch.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INTEROP_DIR="$ROOT/interop-tests/m1"
mkdir -p "$INTEROP_DIR"

echo "==> phase 1: Go generates fixtures"
(cd "$ROOT/fabric-sdk-go" && go run ./cmd/interop -mode=generate -dir "$INTEROP_DIR")

echo "==> phase 2: JS generates fixtures"
(cd "$ROOT/fabric-sdk-js" && node interop/cli.js generate "$INTEROP_DIR")

echo "==> phase 3: JS verifies Go fixtures"
(cd "$ROOT/fabric-sdk-js" && node interop/cli.js verify "$INTEROP_DIR")

echo "==> phase 4: Go verifies JS fixtures"
(cd "$ROOT/fabric-sdk-go" && go run ./cmd/interop -mode=verify -dir "$INTEROP_DIR")

echo "M1 interop: OK"
