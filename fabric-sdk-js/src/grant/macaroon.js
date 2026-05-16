// Macaroon mint/parse/verify wrapper. Wire-compatible with the Go SDK via
// gopkg.in/macaroon.v2 / js-macaroon (both implement libmacaroon v2).
// Authoritative caveat catalog: docs/specs/fabric-spec-001-v1.0.md.

import macaroonLib from 'macaroon';
import { bytesToBase64, base64ToBytes } from '../util/base64.js';

const { newMacaroon, importMacaroon } = macaroonLib;

/** libmacaroon wire version used by the fabric protocol. */
export const MACAROON_VERSION = 2;

const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

/**
 * SignatureInvalidError is thrown when the macaroon HMAC chain does not verify.
 */
export class SignatureInvalidError extends Error {
  constructor(message, cause) {
    super(message);
    this.name = 'SignatureInvalidError';
    this.code = 'grant_invalid';
    if (cause) this.cause = cause;
  }
}

/**
 * Mint a Grant. The identifier object is JSON-encoded as the macaroon's id bytes.
 *
 * @param {{
 *   rootKey: Uint8Array,
 *   location: string,
 *   identifier: object,
 *   caveats?: string[]
 * }} spec
 * @returns {{ grantId: string, macaroon: Uint8Array, identifier: object, caveats: string[] }}
 */
export function mint(spec) {
  if (!(spec.rootKey instanceof Uint8Array)) {
    throw new TypeError('grant.mint: rootKey must be Uint8Array');
  }
  // The js-macaroon identifier carries the issued_by_keypair as a Uint8Array;
  // serializing through JSON would lose that, so dehydrate to base64 first.
  const idObj = dehydrate(spec.identifier);
  const idBytes = enc.encode(JSON.stringify(idObj));
  const m = newMacaroon({
    identifier: idBytes,
    location: spec.location ?? '',
    rootKey: spec.rootKey,
    version: MACAROON_VERSION,
  });
  const caveats = spec.caveats ?? [];
  for (const c of caveats) {
    m.addFirstPartyCaveat(enc.encode(c));
  }
  return {
    grantId: spec.identifier.grant_id,
    macaroon: m.exportBinary(),
    identifier: spec.identifier,
    caveats: [...caveats],
  };
}

/**
 * Parse a wire-format macaroon. Extracts the Identifier JSON without verifying
 * the signature; pair with verifySignature for the full check.
 *
 * @param {Uint8Array} macBytes
 * @returns {{ grantId: string, macaroon: Uint8Array, identifier: object, caveats: string[] }}
 */
export function parse(macBytes) {
  const m = importMacaroon(macBytes);
  const idText = dec.decode(m._exportAsJSONObjectV2 ? identifierBytesV2(m) : identifierBytesV1(m));
  const identifier = rehydrate(JSON.parse(idText));
  const caveats = [];
  // The js-macaroon Macaroon class doesn't expose caveats() the same way Go does;
  // we round-trip via the JSON export form which is wire-defined per libmacaroon.
  const exported = m.exportJSON();
  // v2 JSON shape has caveats under "c": [{i: "...", l?, vid?}, ...]
  const cs = exported.c ?? exported.caveats ?? [];
  for (const c of cs) {
    const cond = c.i ?? c.cid ?? '';
    caveats.push(cond);
  }
  return {
    grantId: identifier.grant_id,
    macaroon: macBytes,
    identifier,
    caveats,
  };
}

/**
 * Verify the macaroon's HMAC chain under rootKey. The check function is called
 * for each first-party caveat string; throw from check to reject. Pass
 * alwaysSatisfied to verify only the signature.
 *
 * @param {Uint8Array} macBytes
 * @param {Uint8Array} rootKey
 * @param {(caveat: string) => void} check
 */
export function verifySignature(macBytes, rootKey, check = alwaysSatisfied) {
  const m = importMacaroon(macBytes);
  try {
    m.verify(rootKey, check, []);
  } catch (e) {
    throw new SignatureInvalidError('macaroon signature verification failed', e);
  }
}

/** A no-op check function used to verify signature only. */
export function alwaysSatisfied() {}

// --- identifier helpers ---
//
// macaroon objects in JS hold "_identifier_bytes" but the field is private.
// importMacaroon → exportJSON gives us a v2 JSON form where the id is base64
// in 'i64' or text in 'i'. We use exportJSON to extract the id bytes.

function identifierBytesV2(m) {
  const obj = m.exportJSON();
  if (obj.i64 != null) return base64ToBytes(obj.i64);
  if (obj.i != null) return enc.encode(obj.i);
  throw new Error('macaroon identifier missing from exportJSON');
}

function identifierBytesV1(m) {
  const obj = m.exportJSON();
  if (obj.identifier != null) return enc.encode(obj.identifier);
  throw new Error('macaroon identifier missing (v1)');
}

// dehydrate / rehydrate: convert Uint8Array fields in the identifier to base64
// strings before JSON-encoding, and back on parse. Matches what Go's json
// package does for []byte fields.
function dehydrate(obj) {
  return JSON.parse(
    JSON.stringify(obj, (_, v) => (v instanceof Uint8Array ? bytesToBase64(v) : v)),
  );
}
function rehydrate(obj) {
  if (obj && typeof obj === 'object') {
    if ('issued_by_keypair' in obj && typeof obj.issued_by_keypair === 'string') {
      obj.issued_by_keypair = base64ToBytes(obj.issued_by_keypair);
    }
  }
  return obj;
}
