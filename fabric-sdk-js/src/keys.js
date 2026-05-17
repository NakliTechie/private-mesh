// Per-namespace key derivation. Vault payloads are encrypted with a key
// derived from the FIF's root keypair seed via HKDF-SHA256, salted with the
// fabric protocol name and parameterized by namespace.
//
// Authoritative: docs/specs/fabric-spec-001-v1.0.md §Encryption.

import { deriveKey } from './crypto.js';

const enc = new TextEncoder();
const NAMESPACE_KEY_SALT = enc.encode('naklimesh/1.0/vault-key');

/**
 * Derive the symmetric key used to encrypt payloads in a Vault namespace.
 * The IKM is the Ed25519 private key seed (the first 32 bytes of the
 * RFC8032 expanded private key as stored in the FIF).
 *
 * @param {Uint8Array} rootPrivateKey  64 bytes (Ed25519) — we use the first 32.
 * @param {string} namespace
 * @returns {Promise<Uint8Array>} 32 bytes
 */
export async function deriveVaultKey(rootPrivateKey, namespace) {
  if (!(rootPrivateKey instanceof Uint8Array) || rootPrivateKey.length < 32) {
    throw new Error('keys.deriveVaultKey: rootPrivateKey must be ≥32 bytes');
  }
  if (!namespace) throw new Error('keys.deriveVaultKey: namespace required');
  const seed = rootPrivateKey.slice(0, 32);
  const info = enc.encode(`vault:${namespace}`);
  return deriveKey(seed, NAMESPACE_KEY_SALT, info);
}
