#!/usr/bin/env bash
# build-all.sh — run every subdirectory's smoke test in order.
# M0 gate artifact: prints OK for each subdir and exits 0.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SUBDIRS=(
  fabric-sdk-go
  fabric-sdk-js
  fabric-merge-helpers
  nakli-hub
  nakli-cf-worker
  nakli-cli
  nakli-local-bridge
)

fail=0
for d in "${SUBDIRS[@]}"; do
  if [[ ! -x "$d/smoke.sh" ]]; then
    echo "MISSING: $d/smoke.sh is not present or not executable"
    fail=1
    continue
  fi
  if ! "./$d/smoke.sh"; then
    echo "FAIL: $d"
    fail=1
  fi
done

if [[ $fail -ne 0 ]]; then
  echo "build-all: one or more smoke tests failed"
  exit 1
fi

echo "build-all: OK (${#SUBDIRS[@]} subdirectories)"
