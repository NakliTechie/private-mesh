// Cryptographic primitives used by the Private Mesh fabric SDK.
//
// Authoritative wire details: docs/specs/fabric-spec-001-v1.0.md §"Encryption"
// and §"Fabric Identity File". Defaults here match the Go SDK
// (fabric-sdk-go/crypto) so artifacts produced by either side round-trip.

import { xchacha20poly1305 } from '@noble/ciphers/chacha';
import { argon2id } from 'hash-wasm';

/** Symmetric key length used everywhere (32 bytes). */
export const KEY_SIZE = 32;
/** XChaCha20-Poly1305 nonce length (24 bytes). */
export const NONCE_SIZE = 24;
/** Poly1305 authentication tag length (16 bytes). */
export const TAG_SIZE = 16;
/** FIF envelope salt length (16 bytes per fabric-spec). */
export const SALT_SIZE = 16;

/** Default Argon2id params for FIF passphrase-only envelopes (matches Go SDK). */
export const DEFAULT_ARGON2ID_PARAMS = Object.freeze({
  time: 3, // t_cost
  memory: 65536, // m_cost in KiB
  parallelism: 4,
  hashLength: KEY_SIZE,
});

/**
 * Encrypt plaintext under XChaCha20-Poly1305.
 *
 * @param {Uint8Array} key   32-byte key.
 * @param {Uint8Array} nonce 24-byte nonce.
 * @param {Uint8Array} plaintext
 * @param {Uint8Array|null} aad Additional authenticated data (or null).
 * @returns {Uint8Array} ciphertext concatenated with the 16-byte tag.
 */
export function seal(key, nonce, plaintext, aad) {
  if (key.length !== KEY_SIZE) throw new Error('fabric/crypto: key must be 32 bytes');
  if (nonce.length !== NONCE_SIZE) throw new Error('fabric/crypto: nonce must be 24 bytes');
  const cipher = xchacha20poly1305(key, nonce, aad ?? undefined);
  return cipher.encrypt(plaintext);
}

/**
 * Decrypt and authenticate a Seal output. Throws on authentication failure.
 *
 * @param {Uint8Array} key
 * @param {Uint8Array} nonce
 * @param {Uint8Array} ciphertext  ciphertext || tag, as produced by seal.
 * @param {Uint8Array|null} aad
 * @returns {Uint8Array} plaintext
 */
export function open(key, nonce, ciphertext, aad) {
  if (key.length !== KEY_SIZE) throw new Error('fabric/crypto: key must be 32 bytes');
  if (nonce.length !== NONCE_SIZE) throw new Error('fabric/crypto: nonce must be 24 bytes');
  const cipher = xchacha20poly1305(key, nonce, aad ?? undefined);
  return cipher.decrypt(ciphertext);
}

/** Return n cryptographically-random bytes using WebCrypto. */
export function randomBytes(n) {
  const out = new Uint8Array(n);
  globalThis.crypto.getRandomValues(out);
  return out;
}

/** 24 fresh random bytes suitable for XChaCha20-Poly1305 nonces. */
export const randomNonce = () => randomBytes(NONCE_SIZE);
/** 16 fresh random bytes suitable for Argon2id salts. */
export const randomSalt = () => randomBytes(SALT_SIZE);

/**
 * Derive a 32-byte symmetric key from a passphrase using Argon2id.
 *
 * @param {string} passphrase
 * @param {Uint8Array} salt
 * @param {{time: number, memory: number, parallelism: number, hashLength: number}} [params]
 * @returns {Promise<Uint8Array>} 32-byte derived key.
 */
export async function deriveKeyArgon2id(passphrase, salt, params = DEFAULT_ARGON2ID_PARAMS) {
  const result = await argon2id({
    password: passphrase,
    salt,
    iterations: params.time,
    memorySize: params.memory,
    parallelism: params.parallelism,
    hashLength: params.hashLength,
    outputType: 'binary',
  });
  return result instanceof Uint8Array ? result : new Uint8Array(result);
}

/**
 * Derive a 32-byte symmetric key via HKDF-SHA256.
 *
 * @param {Uint8Array} secret
 * @param {Uint8Array} salt   May be empty; HKDF treats empty as zero-string of HashLen.
 * @param {Uint8Array} info
 * @returns {Promise<Uint8Array>}
 */
export async function deriveKey(secret, salt, info) {
  const key = await globalThis.crypto.subtle.importKey(
    'raw',
    secret,
    { name: 'HKDF' },
    false,
    ['deriveBits'],
  );
  const bits = await globalThis.crypto.subtle.deriveBits(
    { name: 'HKDF', hash: 'SHA-256', salt, info },
    key,
    KEY_SIZE * 8,
  );
  return new Uint8Array(bits);
}
