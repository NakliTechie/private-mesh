// History API — append (hash-chained), read, verify. Spec §History API.
//
// Unlike Vault, History events are not encrypted client-side — they're public
// audit-log records. Callers pass plaintext payload; the SDK serializes it and
// sends. The hash chain is managed by the SDK: it tracks the last known head
// per stream and supplies previous_event_hash on each append.

import { newIdempotencyKey } from './transport.js';
import { bytesToBase64, base64ToBytes } from './util/base64.js';

const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

export class HistoryAPI {
  constructor(opts) {
    this._transports = opts.transports;
    this._getCurrentGrant = opts.getCurrentGrant;
    this._freshness = opts.freshness;
    /** @type {Map<string, string>} streamId → last known head_event_hash (base64) */
    this._heads = new Map();
  }

  /**
   * Append an event to a History stream.
   * @param {{ streamId: string, event: { kind: string, payload: any }, idempotencyKey?: string, grant?: string, previousEventHash?: string|null }} spec
   */
  async append(spec) {
    if (!spec?.streamId || !spec?.event?.kind) {
      throw new TypeError('history.append: streamId and event.kind are required');
    }
    const prev = spec.previousEventHash ?? this._heads.get(spec.streamId) ?? '';
    const body = {
      stream_id: spec.streamId,
      event: {
        kind: spec.event.kind,
        payload_ciphertext: bytesToBase64(encodePayload(spec.event.payload)),
        causal_dependencies: spec.event.causalDependencies ?? [],
        vector_clock: spec.event.vectorClock ?? {},
        previous_event_hash: prev,
      },
    };
    const t = this._transports.pick();
    const res = await t.do('POST', '/fabric/v1/history/append', {
      body,
      grant: spec.grant ?? this._getCurrentGrant(),
      idempotencyKey: spec.idempotencyKey ?? newIdempotencyKey(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    this._heads.set(spec.streamId, res.data.event_hash);
    return {
      eventId: res.data.event_id,
      eventHash: res.data.event_hash,
      sequenceNumber: res.data.sequence_number,
    };
  }

  async read(streamId, opts = {}) {
    const qs = [];
    if (opts.sinceEventId) qs.push(`since=${encodeURIComponent(opts.sinceEventId)}`);
    if (opts.limit) qs.push(`limit=${opts.limit}`);
    const path = `/fabric/v1/history/stream/${encodeURIComponent(streamId)}`
      + (qs.length ? `?${qs.join('&')}` : '');
    const t = this._transports.pick();
    const res = await t.do('GET', path, {
      grant: opts.grant ?? this._getCurrentGrant(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    return {
      events: (res.data?.events ?? []).map((ev) => ({
        eventId: ev.event_id,
        kind: ev.kind,
        sequenceNumber: ev.sequence_number,
        payload: decodePayload(base64ToBytes(ev.payload_ciphertext)),
        previousEventHash: ev.previous_event_hash,
        eventHash: ev.event_hash,
        appendedAt: ev.appended_at ? new Date(ev.appended_at) : null,
      })),
      more: !!res.data?.more,
    };
  }

  async verify(streamId, opts = {}) {
    const t = this._transports.pick();
    const res = await t.do('GET', `/fabric/v1/history/verify/${encodeURIComponent(streamId)}`, {
      grant: opts.grant ?? this._getCurrentGrant(),
    });
    this._freshness?._updateFromEnvelope(res.freshness);
    return {
      verified: !!res.data?.verified,
      length: res.data?.length ?? 0,
      headHash: res.data?.head_hash ?? '',
      brokenAtEventId: res.data?.broken_at_event_id ?? null,
    };
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
  try { return JSON.parse(dec.decode(bytes)); }
  catch {
    try { return dec.decode(bytes); }
    catch { return bytes; }
  }
}
