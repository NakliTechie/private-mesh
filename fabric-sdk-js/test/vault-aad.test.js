import { describe, it, before } from 'node:test';
import assert from 'node:assert/strict';
import { seal, open, randomNonce } from '../src/crypto.js';

// We can't easily test VaultAPI.append/read end-to-end without a
// transport mock, but the security-critical bit is the AAD construction
// — assert that ciphertexts bound to (namespace, stream_id,
// vector_clock) cannot be opened with a different binding.

const enc = new TextEncoder();

async function vaultEventAAD(namespace, streamId, vectorClock) {
  const sep = new Uint8Array([0x1F]);
  const vcJSON = JSON.stringify(vectorClock ?? {});
  const buf = concat(
    enc.encode(namespace),
    sep,
    enc.encode(streamId),
    sep,
    enc.encode(vcJSON),
  );
  const digest = await crypto.subtle.digest('SHA-256', buf);
  return new Uint8Array(digest);
}

function concat(...parts) {
  let total = 0;
  for (const p of parts) total += p.byteLength;
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.byteLength; }
  return out;
}

describe('vault payload AAD binding (P2 #21)', () => {
  let key;
  before(() => {
    // Fixed key for reproducibility.
    key = new Uint8Array(32);
    for (let i = 0; i < 32; i++) key[i] = i;
  });

  it('open succeeds with matching AAD', async () => {
    const nonce = randomNonce();
    const plaintext = enc.encode('hello');
    const aad = await vaultEventAAD('ns-A', 'stream-1', { hubA: 1 });
    const cipher = seal(key, nonce, plaintext, aad);
    const got = open(key, nonce, cipher, aad);
    assert.equal(new TextDecoder().decode(got), 'hello');
  });

  it('open fails when namespace is swapped', async () => {
    const nonce = randomNonce();
    const plaintext = enc.encode('secret');
    const aadOrig = await vaultEventAAD('ns-A', 'stream-1', { hubA: 1 });
    const cipher = seal(key, nonce, plaintext, aadOrig);
    const aadSwapped = await vaultEventAAD('ns-B', 'stream-1', { hubA: 1 });
    assert.throws(() => open(key, nonce, cipher, aadSwapped));
  });

  it('open fails when stream_id is swapped', async () => {
    const nonce = randomNonce();
    const plaintext = enc.encode('secret');
    const aadOrig = await vaultEventAAD('ns-A', 'stream-1', {});
    const cipher = seal(key, nonce, plaintext, aadOrig);
    const aadSwapped = await vaultEventAAD('ns-A', 'stream-2', {});
    assert.throws(() => open(key, nonce, cipher, aadSwapped));
  });

  it('open fails when vector_clock is swapped', async () => {
    const nonce = randomNonce();
    const plaintext = enc.encode('secret');
    const aadOrig = await vaultEventAAD('ns', 's', { a: 1 });
    const cipher = seal(key, nonce, plaintext, aadOrig);
    const aadSwapped = await vaultEventAAD('ns', 's', { a: 2 });
    assert.throws(() => open(key, nonce, cipher, aadSwapped));
  });

  it('AAD is stable across calls (deterministic)', async () => {
    const a = await vaultEventAAD('ns', 's', { x: 1 });
    const b = await vaultEventAAD('ns', 's', { x: 1 });
    assert.deepEqual(a, b);
  });
});
