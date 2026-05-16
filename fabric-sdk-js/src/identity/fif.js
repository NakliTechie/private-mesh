// FIF parse/unlock/serialize. Wire format spec:
//   docs/specs/fabric-spec-001-v1.0.md §"Fabric Identity File"
//
// Interop note: AAD for the envelope's AEAD is the on-wire envelope-header
// bytes (4-byte big-endian length + UTF-8 JSON). The Go SDK does the same;
// fabric-sdk-go/identity/fif.go documents the binding.

import {
  seal,
  open,
  randomNonce,
  randomSalt,
  deriveKeyArgon2id,
  DEFAULT_ARGON2ID_PARAMS,
  SALT_SIZE,
  NONCE_SIZE,
  TAG_SIZE,
} from '../crypto.js';
import { bytesToBase64, base64ToBytes } from '../util/base64.js';

export const FIF_FORMAT = 'fif/1.0';
export const INNER_FIF_FORMAT = 'fif-inner/1.0';

/** v1.0 envelope types: only "passphrase-only" is accepted; rest are reserved. */
export const ENVELOPE_PASSPHRASE_ONLY = 'passphrase-only';
export const RESERVED_ENVELOPE_TYPES = Object.freeze([
  'shamir-shares',
  'device-quorum',
  'social-recovery',
]);

/**
 * FIFError carries a protocol-aligned code so callers can branch on
 * fif_format / fif_auth / fif_envelope_unsupported / identity_locked.
 */
export class FIFError extends Error {
  constructor(code, message, cause) {
    super(message);
    this.name = 'FIFError';
    this.code = code;
    if (cause) this.cause = cause;
  }
}

const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

// Inner-FIF binary fields that travel as base64 strings on the wire. Helpers
// below convert them to Uint8Array on parse and back to base64 on serialize.
const SCALAR_BINARY_PATHS = [
  ['root_keypair', 'public_key'],
  ['root_keypair', 'private_key'],
];
const ARRAY_BINARY_PATHS = [
  ['device_subkeys', 'public_key'],
  ['device_subkeys', 'private_key'],
  ['agent_identities', 'public_key'],
  ['agent_identities', 'private_key'],
  ['transports', 'public_key'],
  ['grants_held', 'macaroon'],
];

function reviveInnerBinary(inner) {
  for (const [parent, field] of SCALAR_BINARY_PATHS) {
    const v = inner?.[parent]?.[field];
    if (typeof v === 'string') inner[parent][field] = base64ToBytes(v);
  }
  for (const [arr, field] of ARRAY_BINARY_PATHS) {
    if (Array.isArray(inner?.[arr])) {
      for (const item of inner[arr]) {
        if (typeof item?.[field] === 'string') item[field] = base64ToBytes(item[field]);
      }
    }
  }
  return inner;
}

function dehydrateInnerBinary(inner) {
  // Return a deep-cloned representation where Uint8Array fields are base64
  // strings, so JSON.stringify produces a wire-compatible body.
  const out = JSON.parse(
    JSON.stringify(inner, (_, v) => {
      if (v instanceof Uint8Array) return bytesToBase64(v);
      return v;
    }),
  );
  return out;
}

/** Build an InnerFIF skeleton with collections initialized to empties. */
export function newInnerFIF(principal, rootKeypair) {
  return {
    format: INNER_FIF_FORMAT,
    principal,
    root_keypair: rootKeypair,
    device_subkeys: [],
    agent_identities: [],
    transports: [],
    grants_held: [],
    bridge_credentials: [],
    recent_state_cache: {
      vault_heads: {},
      history_heads: {},
    },
  };
}

/** Build a parsed FIF in memory with a fresh passphrase-only envelope. */
export async function newFIF(passphrase, inner) {
  if (!inner) throw new FIFError('fif_format', 'inner is required');
  const salt = randomSalt();
  const nonce = randomNonce();
  const params = DEFAULT_ARGON2ID_PARAMS;
  const key = await deriveKeyArgon2id(passphrase, salt, params);
  const header = {
    format: FIF_FORMAT,
    envelope_type: ENVELOPE_PASSPHRASE_ONLY,
    envelope_params: {
      kdf: 'argon2id',
      kdf_params: {
        m_cost: params.memory,
        t_cost: params.time,
        parallelism: params.parallelism,
      },
      salt: bytesToBase64(salt),
      nonce: bytesToBase64(nonce),
    },
  };
  return new FIF({ header, key, inner, salt, nonce });
}

/**
 * ParseFIF reads the envelope header and retains the encrypted body. Call
 * unlock() to decrypt. Throws FIFError with code "fif_envelope_unsupported"
 * if the envelope_type is reserved or unknown.
 *
 * @param {Uint8Array} bytes
 * @returns {FIF}
 */
export function parseFIF(bytes) {
  if (bytes.length < 4) {
    throw new FIFError('fif_format', 'too short for length prefix');
  }
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const headerLen = view.getUint32(0, false /* big-endian */);
  if (headerLen === 0 || headerLen > 1 << 20) {
    throw new FIFError('fif_format', `header length out of range: ${headerLen}`);
  }
  if (bytes.length < 4 + headerLen + TAG_SIZE) {
    throw new FIFError('fif_format', 'body shorter than AEAD tag');
  }
  const headerJSON = bytes.subarray(4, 4 + headerLen);
  const body = bytes.subarray(4 + headerLen);
  let header;
  try {
    header = JSON.parse(dec.decode(headerJSON));
  } catch (e) {
    throw new FIFError('fif_format', 'header JSON parse failed', e);
  }
  if (header.format !== FIF_FORMAT) {
    throw new FIFError('fif_format', `unknown format ${JSON.stringify(header.format)}`);
  }
  if (RESERVED_ENVELOPE_TYPES.includes(header.envelope_type)) {
    throw new FIFError(
      'fif_envelope_unsupported',
      `envelope_type ${JSON.stringify(header.envelope_type)} is reserved for v1.x`,
    );
  }
  if (header.envelope_type !== ENVELOPE_PASSPHRASE_ONLY) {
    throw new FIFError(
      'fif_envelope_unsupported',
      `envelope_type ${JSON.stringify(header.envelope_type)} is not supported`,
    );
  }
  if (header.envelope_params?.kdf !== 'argon2id') {
    throw new FIFError('fif_format', `unsupported kdf ${header.envelope_params?.kdf}`);
  }
  const salt = base64ToBytes(header.envelope_params.salt);
  const nonce = base64ToBytes(header.envelope_params.nonce);
  if (salt.length !== SALT_SIZE) {
    throw new FIFError('fif_format', `salt length ${salt.length}, want ${SALT_SIZE}`);
  }
  if (nonce.length !== NONCE_SIZE) {
    throw new FIFError('fif_format', `nonce length ${nonce.length}, want ${NONCE_SIZE}`);
  }
  // Retain the raw header bytes as AAD (the bytes the issuer used, verbatim).
  const headerBytes = bytes.subarray(0, 4 + headerLen);
  return new FIF({ header, headerBytes, body, salt, nonce });
}

/** Internal: assembles a 4-byte big-endian length prefix + bytes. */
function withLengthPrefix(jsonBytes) {
  const out = new Uint8Array(4 + jsonBytes.length);
  new DataView(out.buffer).setUint32(0, jsonBytes.length, false);
  out.set(jsonBytes, 4);
  return out;
}

export class FIF {
  /**
   * @param {{header: object, key?: Uint8Array, inner?: object,
   *          headerBytes?: Uint8Array, body?: Uint8Array,
   *          salt: Uint8Array, nonce: Uint8Array}} parts
   */
  constructor(parts) {
    this.header = parts.header;
    this.key = parts.key ?? null;
    this.inner = parts.inner ?? null;
    this.headerBytes = parts.headerBytes ?? null;
    this.body = parts.body ?? null;
    this.salt = parts.salt;
    this.nonce = parts.nonce;
  }

  get envelopeType() {
    return this.header.envelope_type;
  }

  isUnlocked() {
    return this.inner !== null && this.key !== null;
  }

  async unlock(passphrase) {
    if (this.isUnlocked()) return;
    if (!this.body || !this.headerBytes) {
      throw new FIFError('fif_format', 'cannot unlock — FIF was not parsed from bytes');
    }
    const p = this.header.envelope_params.kdf_params;
    const key = await deriveKeyArgon2id(passphrase, this.salt, {
      time: p.t_cost,
      memory: p.m_cost,
      parallelism: p.parallelism,
      hashLength: 32,
    });
    let plaintext;
    try {
      plaintext = open(key, this.nonce, this.body, this.headerBytes);
    } catch (e) {
      throw new FIFError('fif_auth', 'FIF authentication failed', e);
    }
    let inner;
    try {
      inner = JSON.parse(dec.decode(plaintext));
    } catch (e) {
      throw new FIFError('fif_format', 'parse inner JSON', e);
    }
    if (inner.format !== INNER_FIF_FORMAT) {
      throw new FIFError('fif_format', `unknown inner format ${inner.format}`);
    }
    reviveInnerBinary(inner);
    this.key = key;
    this.inner = inner;
  }

  serialize() {
    if (!this.isUnlocked()) {
      throw new FIFError('identity_locked', 'FIF is locked');
    }
    const innerWire = dehydrateInnerBinary(this.inner);
    const innerJSON = enc.encode(JSON.stringify(innerWire));
    const headerJSON = enc.encode(JSON.stringify(this.header));
    const headerBytes = withLengthPrefix(headerJSON);
    const ciphertext = seal(this.key, this.nonce, innerJSON, headerBytes);
    const out = new Uint8Array(headerBytes.length + ciphertext.length);
    out.set(headerBytes, 0);
    out.set(ciphertext, headerBytes.length);
    this.headerBytes = headerBytes;
    this.body = ciphertext;
    return out;
  }

  lock() {
    if (this.key) this.key.fill(0);
    this.key = null;
    this.inner = null;
  }
}
