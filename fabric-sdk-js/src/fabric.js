// Top-level Fabric class. Spec §Top-level API. M5 wires the protocol-speaking
// surface (vault, history, grants, sync/llm/bridge stubs, freshness, health)
// plus FIF lifecycle that already shipped in M1. Browser-only concerns
// (IndexedDB queue, Web Locks leader election, SSE subscribe) land in M5.x.

import {
  parseFIF, newFIF, newInnerFIF,
  ENVELOPE_PASSPHRASE_ONLY,
} from './identity/fif.js';
import { EventBus } from './events.js';
import { HubTransport, TransportManager } from './transport.js';
import { FreshnessAPI } from './freshness.js';
import { HealthAPI } from './health.js';
import { GrantStore } from './grants.js';
import { VaultAPI } from './vault.js';
import { HistoryAPI } from './history.js';
import { SyncAPI, LLMAPI, BridgeAPI } from './stubs.js';
import { IdentityLockedError, FIFEnvelopeUnsupportedError } from './errors.js';

/**
 * @typedef {Object} FabricOptions
 * @property {{ url: string, id?: string, fetch?: typeof fetch, timeoutMs?: number, preference?: number }[]} [transports]
 * @property {number} [stalenessBudgetMs]
 * @property {(typeof fetch)} [fetch]
 */

export class Fabric {
  /** @param {FabricOptions} [options] */
  constructor(options = {}) {
    this.options = options;
    /** @type {import('./identity/fif.js').FIF|null} */
    this._fif = null;
    /** @type {Uint8Array|null} Direct root-seed injection for test / bootstrap flows. */
    this._rootSeed = null;
    /** @type {string|undefined} current grant macaroon (base64) */
    this._currentGrant = undefined;

    this.events = new EventBus();
    this.transports = new TransportManager({
      transports: (options.transports ?? []).map((cfg) => new HubTransport({
        fetch: options.fetch,
        ...cfg,
      })),
    });
    this.freshness = new FreshnessAPI({ stalenessBudgetMs: options.stalenessBudgetMs });
    this.health = new HealthAPI({ transports: this.transports });
    this.grants = new GrantStore({
      transports: this.transports,
      getCurrentGrant: () => this._currentGrant,
    });
    this.vault = new VaultAPI({
      transports: this.transports,
      getRootSeed: () => this._getRootSeed(),
      getCurrentGrant: () => this._currentGrant,
      freshness: this.freshness,
      events: this.events,
    });
    this.history = new HistoryAPI({
      transports: this.transports,
      getCurrentGrant: () => this._currentGrant,
      freshness: this.freshness,
    });
    this.sync = new SyncAPI({
      transports: this.transports,
      getCurrentGrant: () => this._currentGrant,
    });
    this.llm = new LLMAPI({
      transports: this.transports,
      getCurrentGrant: () => this._currentGrant,
    });
    this.bridge = new BridgeAPI({
      transports: this.transports,
      getCurrentGrant: () => this._currentGrant,
    });
  }

  /** Get the current identity (null when locked). */
  get identity() {
    return this._fif?.inner ? identityFromInner(this._fif.inner) : null;
  }

  /**
   * Unlock a FIF and load the identity into memory.
   *
   * @param {ArrayBuffer|Uint8Array} fifBytes
   * @param {string} passphrase
   */
  async unlockFIF(fifBytes, passphrase) {
    const bytes = fifBytes instanceof Uint8Array ? fifBytes : new Uint8Array(fifBytes);
    const fif = parseFIF(bytes);
    if (fif.envelopeType !== ENVELOPE_PASSPHRASE_ONLY) {
      throw new FIFEnvelopeUnsupportedError(`envelope ${fif.envelopeType} not supported in v1.0`);
    }
    await fif.unlock(passphrase);
    this._fif = fif;
    return this.identity;
  }

  /**
   * Generate a new FIF and load it.
   *
   * @param {string} passphrase
   * @param {string} displayName
   * @param {{ rootKeypair: { algorithm: string, public_key: Uint8Array, private_key: Uint8Array }, principalId: string }} seed
   *        The caller supplies the root keypair (typically generated via
   *        WebCrypto Ed25519 or out-of-band) and a freshly-allocated principal
   *        id. The SDK doesn't generate Ed25519 keys itself in M5 — that's
   *        deferred until the SDK adds a small @noble/ed25519 dep at M5.x.
   */
  async createFIF(passphrase, displayName, seed) {
    if (!seed?.rootKeypair?.private_key) {
      throw new TypeError('createFIF: seed.rootKeypair.private_key is required');
    }
    const principal = {
      type: 'human',
      id: seed.principalId,
      display_name: displayName,
      created_at: new Date().toISOString(),
    };
    const inner = newInnerFIF(principal, seed.rootKeypair);
    const fif = await newFIF(passphrase, inner);
    this._fif = fif;
    return { fif: fif.serialize(), identity: this.identity };
  }

  /** Forget the unlocked FIF and any cached vault keys. */
  lock() {
    if (this._fif?.isUnlocked()) this._fif.lock();
    this._fif = null;
    this._rootSeed = null;
    this._currentGrant = undefined;
    this.vault._flushKeys();
  }

  /**
   * Set the "current grant" the SDK will use to authenticate operations. The
   * macaroon is base64-encoded wire bytes (same shape the Hub returns from
   * /grant/mint).
   *
   * @param {string} macaroonB64
   */
  useGrant(macaroonB64) {
    this._currentGrant = macaroonB64;
  }

  /** Returns the macaroon currently used for X-Fabric-Grant. */
  currentGrant() {
    return this._currentGrant;
  }

  /**
   * Test / bootstrap path: inject a raw 32-byte root seed for vault
   * encryption without going through the full FIF unlock. Real apps unlock a
   * FIF; this is for the M5 browser gate where the test page receives a
   * paired-out-of-band seed.
   *
   * @param {Uint8Array} seed32
   */
  _useRootSeed(seed32) {
    if (!(seed32 instanceof Uint8Array) || seed32.length < 32) {
      throw new Error('_useRootSeed: must be ≥32 bytes');
    }
    this._rootSeed = seed32;
    // Cached vault keys are stale.
    this.vault._flushKeys();
  }

  /** Convenience: low-level Hub probe. */
  async discover() {
    const t = this.transports.pick();
    const res = await t.do('GET', '/fabric/v1/discover');
    this.freshness._updateFromEnvelope(res.freshness);
    return res.data;
  }

  // --- internal ---

  _getRootSeed() {
    if (this._rootSeed) return this._rootSeed;
    const pk = this._fif?.inner?.root_keypair?.private_key;
    return pk ?? null;
  }

  _assertUnlocked() {
    if (!this._getRootSeed()) throw new IdentityLockedError();
  }
}

function identityFromInner(inner) {
  return {
    principalId: inner.principal?.id,
    principalType: inner.principal?.type ?? 'human',
    publicKey: inner.root_keypair?.public_key,
    rootKeypair: inner.root_keypair,
    devices: inner.device_subkeys ?? [],
    agents: inner.agent_identities ?? [],
    displayName: inner.principal?.display_name,
    createdAt: inner.principal?.created_at ? new Date(inner.principal.created_at) : null,
  };
}
