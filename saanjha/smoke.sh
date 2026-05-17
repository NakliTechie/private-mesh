#!/usr/bin/env bash
# Smoke test for saanjha. As of M8 session 1 this:
# - Asserts saanjha.html exists and is a single self-contained file
# - Verifies no external <script src> (single-HTML constraint per spec)
# - Reports its byte size (target ≤ 150 KB cold-start)
set -euo pipefail
cd "$(dirname "$0")"

if [[ ! -f saanjha.html ]]; then
  echo "FAIL: saanjha.html missing"; exit 1
fi
size=$(wc -c < saanjha.html | tr -d ' ')
if (( size > 200000 )); then
  echo "FAIL: saanjha.html is ${size} bytes — spec targets ≤ 150 KB cold-start"
  exit 1
fi
if grep -E '<script[^>]+src=' saanjha.html >/dev/null; then
  echo "FAIL: saanjha.html has external <script src=...> — session 1 is single-file"
  exit 1
fi
echo "OK: saanjha (saanjha.html ${size} bytes, single-file)"
