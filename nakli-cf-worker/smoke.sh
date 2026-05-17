#!/usr/bin/env bash
# Smoke test for nakli-cf-worker. As of M6 this runs TypeScript type-checking
# over the source. The full end-to-end conformance gate against `wrangler dev`
# is at ../scripts/worker-gate.sh.
set -euo pipefail
cd "$(dirname "$0")"
if [[ ! -d node_modules ]]; then
  pnpm install --silent > /dev/null 2>&1 || true
fi
pnpm exec tsc --noEmit
echo "OK: nakli-cf-worker (typecheck clean)"
