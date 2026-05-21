// Cryptographically random n-digit numeric code, zero-padded.
//
// Replaces an earlier Math.random-based implementation. Math.random is not a
// CSPRNG; observing a few pairing codes could let an attacker predict the
// next one and race a /pair/complete call. The Hub's Go equivalent
// (newNumericCode in handlers_identity.go) uses crypto/rand.Int — this is
// the JS parallel using Web Crypto and rejection sampling to remove modulo
// bias.
export function newNumericCode(n: number): string {
  if (n <= 0 || n > 9) {
    throw new RangeError('newNumericCode: n out of range');
  }
  let max = 1;
  for (let i = 0; i < n; i++) max *= 10;
  // Rejection sampling: drop the last partial bucket so every code in
  // [0, max) has equal probability.
  const limit = Math.floor(0x100000000 / max) * max;
  const buf = new Uint32Array(1);
  let v: number;
  do {
    crypto.getRandomValues(buf);
    v = buf[0];
  } while (v >= limit);
  return (v % max).toString().padStart(n, '0');
}
