// GrantStore — mint / verify / revoke / list / get Grants. Spec §Grant store.
//
// M5 minting goes through the Hub's POST /grant/mint (the SDK doesn't ship
// with the Hub's macaroon root key). Verify is done via POST /grant/verify;
// inspection is local via grant/macaroon.parse.

import { mint as macMint, parse as macParse } from './grant/macaroon.js';
import { bytesToBase64 } from './util/base64.js';
import { newIdempotencyKey } from './transport.js';
import { ulid } from 'ulidx';

/**
 * @typedef {Object} GrantSpec
 * @property {string} recipientPrincipalId
 * @property {{ primitive: string, namespace: string, operations: string[] }} scope
 * @property {Array<object>} caveats   structured caveats (see spec)
 * @property {Date|string} [expiresAt]
 * @property {string} [parentGrant]   base64; optional parent for delegation
 */

export class GrantStore {
  /**
   * @param {{ transports: import('./transport.js').TransportManager, getCurrentGrant: () => string|undefined }} opts
   */
  constructor(opts) {
    this._transports = opts.transports;
    this._getCurrentGrant = opts.getCurrentGrant;
    /** @type {Map<string, { grantId: string, macaroon: string, identifier: object, caveats: string[] }>} */
    this._cache = new Map();
  }

  /**
   * Mint a Grant. The caller's "current grant" — typically a wildcard grant
   * loaded into the Fabric instance — authenticates the mint request.
   *
   * @param {GrantSpec} spec
   */
  async mint(spec) {
    const t = this._transports.pick();
    const body = {
      recipient_principal_id: spec.recipientPrincipalId,
      scope: {
        primitive: spec.scope.primitive,
        namespace: spec.scope.namespace,
        operations: spec.scope.operations,
      },
      caveats: (spec.caveats ?? []).map(caveatToString),
    };
    if (spec.expiresAt) {
      body.expires_at = (spec.expiresAt instanceof Date)
        ? spec.expiresAt.toISOString()
        : new Date(spec.expiresAt).toISOString();
    }
    if (spec.parentGrant) body.parent_grant_macaroon = spec.parentGrant;
    const res = await t.do('POST', '/fabric/v1/grant/mint', {
      body,
      grant: this._getCurrentGrant(),
      idempotencyKey: newIdempotencyKey(),
    });
    const grant = {
      grantId: res.data.grant_id,
      macaroon: res.data.macaroon,
      identifier: macParse(base64ToBytes(res.data.macaroon)).identifier,
      caveats: macParse(base64ToBytes(res.data.macaroon)).caveats,
    };
    this._cache.set(grant.grantId, grant);
    return grant;
  }

  /** Mint a Grant locally using a known root key. Test-only / bootstrap path. */
  mintLocal({ rootKey, location, identifier, caveats }) {
    const out = macMint({ rootKey, location, identifier, caveats });
    const macaroon = bytesToBase64(out.macaroon);
    const grant = {
      grantId: out.grantId,
      macaroon,
      identifier: out.identifier,
      caveats: out.caveats,
    };
    this._cache.set(out.grantId, grant);
    return grant;
  }

  /**
   * Verify a Grant for a hypothetical operation. Goes through Hub's
   * /grant/verify endpoint.
   */
  async verify(macaroonB64, hypothetical) {
    const t = this._transports.pick();
    const res = await t.do('POST', '/fabric/v1/grant/verify', {
      body: {
        macaroon: macaroonB64,
        hypothetical_operation: hypothetical,
      },
      grant: this._getCurrentGrant(),
    });
    return {
      wouldSucceed: !!res.data.would_succeed,
      reasons: res.data.reasons ?? [],
    };
  }

  async revoke(grantId, reason) {
    const t = this._transports.pick();
    await t.do('POST', '/fabric/v1/grant/revoke', {
      body: { grant_id: grantId, reason },
      grant: this._getCurrentGrant(),
      idempotencyKey: newIdempotencyKey(),
    });
    this._cache.delete(grantId);
  }

  /** Inspect a macaroon (base64) locally without verifying its signature. */
  inspect(macaroonB64) {
    return macParse(base64ToBytes(macaroonB64));
  }

  list() {
    return [...this._cache.values()];
  }
  get(grantId) {
    return this._cache.get(grantId);
  }
}

// caveatToString turns a structured Caveat into a wire-format caveat string.
function caveatToString(c) {
  switch (c.type) {
    case 'time-before':
      return `time < ${toRFC3339(c.value)}`;
    case 'time-after':
      return `time > ${toRFC3339(c.value)}`;
    case 'principal-type':
      return `principal-type in [${c.value.join(', ')}]`;
    case 'agent-id':
      return `agent-id == ${c.value}`;
    case 'device-id':
      return `device-id == ${c.value}`;
    case 'operation':
      return `operation in [${c.value.join(', ')}]`;
    case 'namespace':
      return `namespace == ${c.value}`;
    case 'rate':
      return `rate <= ${c.value.count} per ${c.value.window}`;
    case 'max-amount':
      return `max-amount <= ${c.value.amount} ${c.value.currency}`;
    case 'only-domain':
      return `only-domain in [${c.value.join(', ')}]`;
    case 'requires-human-approval':
      return 'requires-human-approval';
    case 'nondelegatable':
      return 'nondelegatable';
    case 'idempotency-required':
      return 'idempotency-required';
    case 'discharge-from':
      return `discharge-from ${c.value}`;
    default:
      throw new Error(`unknown caveat type: ${c.type}`);
  }
}

function toRFC3339(v) {
  if (v instanceof Date) return v.toISOString();
  if (typeof v === 'string') return new Date(v).toISOString();
  throw new Error('time caveat value must be Date or RFC3339 string');
}

function base64ToBytes(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// Unused locally; kept for SDK consumers via re-export.
export { ulid };
