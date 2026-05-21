#!/usr/bin/env bash
# m1-interop-nonce.sh — cross-SDK gate for AEAD nonce freshness on FIF
# re-serialize, in both directions.
#
# Regression scope: fabric-sdk-go/identity/fif.go and
# fabric-sdk-js/src/identity/fif.js previously stored a single AEAD nonce at
# FIF creation time and reused it on every Serialize. This script proves that
# (a) each SDK now generates a fresh nonce on every re-serialize, and (b) the
# OTHER SDK can still decrypt the rotated FIF — i.e., the new nonce is
# correctly bound to the ciphertext via the AAD on both sides.
#
# Phases:
#   1. Go writes from-go/fif.bin
#   2. JS re-serializes from-go/fif.bin -> from-go/fif-rot.bin (new nonce)
#   3. Assert nonces differ across phase 1 and 2 outputs
#   4. Go verifies it can decrypt from-go/fif-rot.bin (proves JS-rotated
#      nonce is bound via AAD when read by Go)
#   5-8. Symmetric: JS writes, Go re-serializes, JS verifies.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INTEROP_DIR="$ROOT/interop-tests/m1"
mkdir -p "$INTEROP_DIR/from-go" "$INTEROP_DIR/from-js"

extract_nonce() {
  python3 - "$1" <<'PY'
import sys, struct, json
with open(sys.argv[1], 'rb') as f:
    b = f.read()
n = struct.unpack('>I', b[:4])[0]
hdr = json.loads(b[4:4+n])
print(hdr['envelope_params']['nonce'])
PY
}

assert_nonces_differ() {
  local before after
  before=$(extract_nonce "$1")
  after=$(extract_nonce "$2")
  if [ "$before" = "$after" ]; then
    echo "FAIL: nonce was not rotated on re-serialize"
    echo "  before: $before"
    echo "  after:  $after"
    exit 1
  fi
  echo "  nonce rotated: $before -> $after"
}

echo "==> phase 1: Go generates baseline fixture"
(cd "$ROOT/fabric-sdk-go" && go run ./cmd/interop -mode=generate -dir "$INTEROP_DIR")

echo "==> phase 2: JS re-serializes Go fixture (rotates nonce)"
(cd "$ROOT/fabric-sdk-js" && node interop/cli.js re-serialize "$INTEROP_DIR/from-go/fif.bin" "$INTEROP_DIR/from-go/fif-rot.bin")

echo "==> phase 3: assert JS rotated the nonce"
assert_nonces_differ "$INTEROP_DIR/from-go/fif.bin" "$INTEROP_DIR/from-go/fif-rot.bin"

echo "==> phase 4: Go verifies JS-rotated fixture (AAD binding survives)"
(cd "$ROOT/fabric-sdk-go" && go run ./cmd/interop -mode=re-serialize -in "$INTEROP_DIR/from-go/fif-rot.bin" -out "$INTEROP_DIR/from-go/fif-rot-go-readable.bin" > /dev/null)

echo "==> phase 5: JS generates baseline fixture"
(cd "$ROOT/fabric-sdk-js" && node interop/cli.js generate "$INTEROP_DIR")

echo "==> phase 6: Go re-serializes JS fixture (rotates nonce)"
(cd "$ROOT/fabric-sdk-go" && go run ./cmd/interop -mode=re-serialize -in "$INTEROP_DIR/from-js/fif.bin" -out "$INTEROP_DIR/from-js/fif-rot.bin")

echo "==> phase 7: assert Go rotated the nonce"
assert_nonces_differ "$INTEROP_DIR/from-js/fif.bin" "$INTEROP_DIR/from-js/fif-rot.bin"

echo "==> phase 8: JS verifies Go-rotated fixture (AAD binding survives)"
(cd "$ROOT/fabric-sdk-js" && node interop/cli.js re-serialize "$INTEROP_DIR/from-js/fif-rot.bin" "$INTEROP_DIR/from-js/fif-rot-js-readable.bin")

echo "M1 nonce interop: OK"
