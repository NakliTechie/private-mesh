// Macaroon parse / verify / mint for the Worker. Wire-compatible with the
// Hub (gopkg.in/macaroon.v2) via the `macaroon` npm package.

import macaroonLib from 'macaroon';

const { newMacaroon, importMacaroon } = macaroonLib;

const enc = new TextEncoder();
const dec = new TextDecoder();

export const MACAROON_VERSION = 2;

export interface GrantIdentifier {
  grant_id: string;
  issued_at: string;
  issued_by_principal: string;
  issued_by_keypair: Uint8Array;
  parent_grant_id?: string;
  scope: {
    primitive: string;
    namespace: string;
    operations: string[];
  };
}

export interface ParsedGrant {
  grantId: string;
  identifier: GrantIdentifier;
  caveats: string[];
  macaroon: Uint8Array;
}

export class SignatureInvalidError extends Error {
  code = 'grant_invalid';
  constructor(message: string, cause?: unknown) {
    super(message);
    this.name = 'SignatureInvalidError';
    if (cause) (this as any).cause = cause;
  }
}

export function bytesToBase64(b: Uint8Array): string {
  let s = '';
  for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
  return btoa(s);
}

export function base64ToBytes(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function dehydrate(obj: any): any {
  return JSON.parse(
    JSON.stringify(obj, (_, v) => (v instanceof Uint8Array ? bytesToBase64(v) : v)),
  );
}
function rehydrate(obj: any): any {
  if (obj && typeof obj === 'object') {
    if (typeof obj.issued_by_keypair === 'string') {
      obj.issued_by_keypair = base64ToBytes(obj.issued_by_keypair);
    }
  }
  return obj;
}

export function mintMacaroon(opts: {
  rootKey: Uint8Array;
  location: string;
  identifier: GrantIdentifier;
  caveats?: string[];
}): { macaroon: Uint8Array; grantId: string } {
  const idObj = dehydrate(opts.identifier);
  const idBytes = enc.encode(JSON.stringify(idObj));
  const m = newMacaroon({
    identifier: idBytes,
    location: opts.location,
    rootKey: opts.rootKey,
    version: MACAROON_VERSION,
  });
  for (const c of opts.caveats ?? []) {
    m.addFirstPartyCaveat(enc.encode(c));
  }
  return { macaroon: m.exportBinary(), grantId: opts.identifier.grant_id };
}

export function parseMacaroon(bytes: Uint8Array): ParsedGrant {
  const m = importMacaroon(bytes);
  const json = m.exportJSON();
  let idBytes: Uint8Array;
  if (json.i64 != null) idBytes = base64ToBytes(json.i64);
  else if (json.i != null) idBytes = enc.encode(json.i);
  else throw new Error('macaroon identifier missing');
  const identifier = rehydrate(JSON.parse(dec.decode(idBytes))) as GrantIdentifier;
  const caveats: string[] = [];
  for (const c of json.c ?? []) {
    caveats.push((c as any).i ?? '');
  }
  return {
    grantId: identifier.grant_id,
    identifier,
    caveats,
    macaroon: bytes,
  };
}

const alwaysSatisfied = (_: string): null => null;

export function verifySignature(
  bytes: Uint8Array,
  rootKey: Uint8Array,
  check: (caveat: string) => Error | null = alwaysSatisfied,
): void {
  const m = importMacaroon(bytes);
  try {
    m.verify(rootKey, (c: string) => {
      const err = check(c);
      if (err) throw err;
    }, []);
  } catch (e) {
    throw new SignatureInvalidError('macaroon signature verification failed', e);
  }
}
