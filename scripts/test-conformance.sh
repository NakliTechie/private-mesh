#!/usr/bin/env bash
# test-conformance.sh — build nakli-hub, start it against a temp data dir on
# a free port, wait for /health, then run the 32-test fabric conformance suite.
# M3 gate artifact.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

tmp=$(mktemp -d -t nakli-conformance.XXXXXX)
trap 'rm -rf "$tmp"; [[ -n "${hub_pid:-}" ]] && kill "$hub_pid" 2>/dev/null || true' EXIT

data_dir="$tmp/hub-data"
log_file="$tmp/hub.log"

# Pick a free port without binding it (Hub will bind it).
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()' 2>/dev/null || echo 17842)
target="http://127.0.0.1:${port}"

echo "==> Building nakli-hub"
(cd nakli-hub && go build -o "$tmp/nakli-hub" ./cmd/nakli-hub)

echo "==> Initializing Hub at $data_dir"
"$tmp/nakli-hub" init --data-dir "$data_dir" >"$tmp/init.log"

echo "==> Starting Hub at $target (with bogus peer for test 26)"
"$tmp/nakli-hub" serve \
  --config "$data_dir/config.json" \
  --listen "127.0.0.1:${port}" \
  --peer-url "http://127.0.0.1:1/unreachable" \
  >"$log_file" 2>&1 &
hub_pid=$!

# Wait for /health.
for i in $(seq 1 50); do
  if curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
if ! curl -fsS "${target}/fabric/v1/health" >/dev/null 2>&1; then
  echo "Hub did not become reachable at $target within 5s"
  echo "--- Hub log ---"
  cat "$log_file"
  exit 1
fi

echo "==> Running conformance suite"
"$tmp/nakli-hub" conformance --target "$target" --data-dir "$data_dir"
exit_code=$?

echo "==> test-conformance done (exit $exit_code)"
exit "$exit_code"
