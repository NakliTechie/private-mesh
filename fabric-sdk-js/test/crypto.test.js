import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  KEY_SIZE,
  NONCE_SIZE,
  TAG_SIZE,
  SALT_SIZE,
  DEFAULT_ARGON2ID_PARAMS,
  seal,
  open,
  randomBytes,
  randomNonce,
  randomSalt,
  deriveKey,
  deriveKeyArgon2id,
} from '../src/crypto.js';

const enc = new TextEncoder();

describe('AEAD (XChaCha20-Poly1305)', () => {
  it('round-trips plaintext', () => {
    const key = randomBytes(KEY_SIZE);
    const nonce = randomNonce();
    const plaintext = enc.encode('Bhai vault payload');
    const aad = enc.encode('namespace=list,stream=001');

    const ct = seal(key, nonce, plaintext, aad);
    assert.equal(ct.length, plaintext.length + TAG_SIZE);

    const pt = open(key, nonce, ct, aad);
    assert.deepEqual(pt, plaintext);
  });

  it('rejects wrong key', () => {
    const k1 = randomBytes(KEY_SIZE);
    const k2 = randomBytes(KEY_SIZE);
    const nonce = randomNonce();
    const ct = seal(k1, nonce, enc.encode('x'), null);
    assert.throws(() => open(k2, nonce, ct, null));
  });

  it('rejects wrong nonce', () => {
    const key = randomBytes(KEY_SIZE);
    const ct = seal(key, randomNonce(), enc.encode('x'), null);
    assert.throws(() => open(key, randomNonce(), ct, null));
  });

  it('rejects tampered ciphertext', () => {
    const key = randomBytes(KEY_SIZE);
    const nonce = randomNonce();
    const ct = seal(key, nonce, enc.encode('hello'), null);
    ct[0] ^= 0x01;
    assert.throws(() => open(key, nonce, ct, null));
  });

  it('rejects wrong AAD', () => {
    const key = randomBytes(KEY_SIZE);
    const nonce = randomNonce();
    const ct = seal(key, nonce, enc.encode('hello'), enc.encode('aad-a'));
    assert.throws(() => open(key, nonce, ct, enc.encode('aad-b')));
  });

  it('rejects bad key length', () => {
    assert.throws(() => seal(new Uint8Array(16), randomNonce(), new Uint8Array(), null));
  });

  it('rejects bad nonce length', () => {
    assert.throws(() => seal(randomBytes(KEY_SIZE), new Uint8Array(12), new Uint8Array(), null));
  });
});

describe('Random sources', () => {
  it('randomNonce is unique across draws', () => {
    const seen = new Set();
    for (let i = 0; i < 64; i++) {
      const n = randomNonce();
      assert.equal(n.length, NONCE_SIZE);
      const k = Buffer.from(n).toString('hex');
      assert.equal(seen.has(k), false, 'duplicate nonce — random source broken');
      seen.add(k);
    }
  });

  it('randomSalt returns 16 bytes', () => {
    assert.equal(randomSalt().length, SALT_SIZE);
  });
});

describe('HKDF-SHA256', () => {
  it('is deterministic', async () => {
    const secret = enc.encode('master-key-material');
    const salt = enc.encode('salt-bytes');
    const info = enc.encode('namespace=list');
    const a = await deriveKey(secret, salt, info);
    const b = await deriveKey(secret, salt, info);
    assert.deepEqual(a, b);
    assert.equal(a.length, KEY_SIZE);
  });

  it('different info → different key', async () => {
    const secret = enc.encode('master-key-material');
    const salt = enc.encode('salt-bytes');
    const a = await deriveKey(secret, salt, enc.encode('namespace=list'));
    const b = await deriveKey(secret, salt, enc.encode('namespace=bahi'));
    assert.notDeepEqual(a, b);
  });

  it('different salt → different key', async () => {
    const secret = enc.encode('master-key-material');
    const info = enc.encode('namespace=list');
    const a = await deriveKey(secret, enc.encode('salt-a'), info);
    const b = await deriveKey(secret, enc.encode('salt-b'), info);
    assert.notDeepEqual(a, b);
  });
});

describe('Argon2id', () => {
  it('is deterministic', async () => {
    const salt = enc.encode('0123456789abcdef');
    const a = await deriveKeyArgon2id('correct horse battery staple', salt);
    const b = await deriveKeyArgon2id('correct horse battery staple', salt);
    assert.deepEqual(a, b);
    assert.equal(a.length, KEY_SIZE);
  });

  it('different passphrase → different key', async () => {
    const salt = enc.encode('0123456789abcdef');
    const a = await deriveKeyArgon2id('pass-a', salt);
    const b = await deriveKeyArgon2id('pass-b', salt);
    assert.notDeepEqual(a, b);
  });

  it('default params match spec (t=3, m=65536, p=4, hash=32)', () => {
    assert.equal(DEFAULT_ARGON2ID_PARAMS.time, 3);
    assert.equal(DEFAULT_ARGON2ID_PARAMS.memory, 65536);
    assert.equal(DEFAULT_ARGON2ID_PARAMS.parallelism, 4);
    assert.equal(DEFAULT_ARGON2ID_PARAMS.hashLength, KEY_SIZE);
  });
});
