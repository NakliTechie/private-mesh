// Vault API — append, read, listStreams, subscribe. Spec §Vault API.
//
// Encryption is transparent: callers pass plaintext to `append`; the SDK
// derives a namespace key from the FIF root keypair seed, encrypts with
// XChaCha20-Poly1305, and sends ciphertext. On read, the SDK decrypts.

import { seal, open as openCipher, randomNonce, NONCE_SIZE } from './crypto.js';
import { deriveVaultKey } from './keys.js';
import { newIdempotencyKey } from './transport.js';
import { bytesToBase64, base64ToBytes } from './util/base64.js';
import { IdentityLockedError, VaultDecryptionError } from './errors.js';

const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

export class VaultAPI {
  /**
   * @param {{
   *   transports: import('./transport.js').TransportManager,
   *   getRootSeed: () => Uint8Array,
   *   getCurrentGrant: () => string,
   *   freshness?: import('./freshness.js').FreshnessAPI,
   *   events?: import('./events.js').EventBus,
   * }} opts
   */
  constructor(opts) {
    this._transports = opts.transports;
    this._getRootSeed = opts.getRootSeed;
    this._getCurrentGrant = opts.getCurrentGrant;
    this._freshness = opts.freshness;
    this._events = opts.events;
    /** @type {Map<string, Uint8Array>} per-namespace key cache */
    this._keys = new Map();
  }

  /**
   * Append an event to a Vault stream.
   * @param {{
   *   namespace: string,
   *   streamId: string,
   *   event: { kind: string, payload: any, causalDependencies?: string[], vectorClock?: Record<string,number> },
   *   idempotencyKey?: string,
   *   grant?: string,
   * }} spec
   */
  async append(spec) {
    if (!spec?.namespace || !spec?.streamId || !spec?.event?.kind) {
      throw new TypeError('vault.append: namespace, streamId, event.kind are required');
    }
    const seed = this._getRootSeed();
    if (!seed) throw new IdentityLockedError();

    const key = await this._getKey(spec.namespace);
    const plaintext = encodePayload(spec.event.payload);
    const nonce = randomNonce();
    const cipher = seal(key, nonce, plaintext, null);
    // Wire format: nonce || ciphertext (so the reader can split).
    const wire = new Uint8Array(NONCE_SIZE + cipher.length);
    wire.set(nonce, 0);
    wire.set(cipher, NONCE_SIZE);

    const body = {
      namespace: spec.namespace,
      stream_id: spec.streamId,
      event: {
        kind: spec.event.kind,
        payload_ciphertext: bytesToBase64(wire),
        payload_metadata: spec.event.payloadMetadata ?? null,
        causal_dependencies: spec.event.causalDependencies ?? [],
        vector_clock: spec.event.vectorClock ?? {},
      },
    };
    const t = this._transports.pick();
    const res = await t.do('POST', '/fabric/v1/vault/append', {
      body,
      grant: spec.grant ?? this._getCurrentGrant(),
      idempotencyKey: spec.idempotencyKey ?? newIdempotencyKey(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    return {
      eventId: res.data.event_id,
      sequenceNumber: res.data.sequence_number,
    };
  }

  /**
   * Read events from a stream. Returns plaintext events.
   * @param {string} namespace
   * @param {string} streamId
   * @param {{ sinceEventId?: string, limit?: number, grant?: string }} [opts]
   */
  async read(namespace, streamId, opts = {}) {
    const seed = this._getRootSeed();
    if (!seed) throw new IdentityLockedError();
    const key = await this._getKey(namespace);

    const qs = [];
    if (opts.sinceEventId) qs.push(`since=${encodeURIComponent(opts.sinceEventId)}`);
    if (opts.limit) qs.push(`limit=${opts.limit}`);
    const path = `/fabric/v1/vault/stream/${encodeURIComponent(namespace)}/${encodeURIComponent(streamId)}`
      + (qs.length ? `?${qs.join('&')}` : '');
    const t = this._transports.pick();
    const res = await t.do('GET', path, {
      grant: opts.grant ?? this._getCurrentGrant(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    const events = [];
    for (const ev of (res.data?.events ?? [])) {
      try {
        const wire = base64ToBytes(ev.payload_ciphertext);
        if (wire.length < NONCE_SIZE) throw new Error('payload too short');
        const nonce = wire.slice(0, NONCE_SIZE);
        const cipher = wire.slice(NONCE_SIZE);
        const plaintext = openCipher(key, nonce, cipher, null);
        events.push({
          eventId: ev.event_id,
          kind: ev.kind,
          sequenceNumber: ev.sequence_number,
          payload: decodePayload(plaintext),
          causalDependencies: ev.causal_dependencies ?? [],
          vectorClock: ev.vector_clock ?? {},
          appendedAt: ev.appended_at ? new Date(ev.appended_at) : null,
          appendedByPrincipal: ev.appended_by_principal,
        });
      } catch (e) {
        const decErr = new VaultDecryptionError(
          `vault.read: failed to decrypt event ${ev.event_id}: ${e?.message ?? e}`,
          { eventId: ev.event_id },
        );
        this._events?.emit('vault-decryption-error', decErr);
        // Surface the event but with no payload, so callers can decide.
        events.push({
          eventId: ev.event_id,
          kind: ev.kind,
          sequenceNumber: ev.sequence_number,
          payload: undefined,
          decryptionError: decErr,
        });
      }
    }
    return { events, more: !!res.data?.more };
  }

  /** List streams in a namespace. */
  async listStreams(namespace, { grant } = {}) {
    const t = this._transports.pick();
    const res = await t.do('GET', `/fabric/v1/vault/streams/${encodeURIComponent(namespace)}`, {
      grant: grant ?? this._getCurrentGrant(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    return res.data?.streams ?? [];
  }

  /**
   * Subscribe to a stream as events arrive (SSE). M5 supports the basic
   * polling-fallback path; native EventSource integration lands in M5.x.
   */
  async *subscribe(/* namespace, streamId, opts */) {
    throw new Error('vault.subscribe: not yet implemented in M5; lands in M5.x (SSE)');
  }

  async _getKey(namespace) {
    if (this._keys.has(namespace)) return this._keys.get(namespace);
    const seed = this._getRootSeed();
    if (!seed) throw new IdentityLockedError();
    const k = await deriveVaultKey(seed, namespace);
    this._keys.set(namespace, k);
    return k;
  }

  /** Test/internal: discard any cached namespace keys (e.g., after lock). */
  _flushKeys() {
    this._keys.clear();
  }
}

function encodePayload(p) {
  if (p == null) return new Uint8Array(0);
  if (p instanceof Uint8Array) return p;
  if (typeof p === 'string') return enc.encode(p);
  return enc.encode(JSON.stringify(p));
}

function decodePayload(bytes) {
  if (bytes.length === 0) return null;
  // Try JSON; fall back to string.
  try {
    return JSON.parse(dec.decode(bytes));
  } catch {
    try {
      return dec.decode(bytes);
    } catch {
      return bytes;
    }
  }
}
