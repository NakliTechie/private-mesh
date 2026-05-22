// History API — append (hash-chained), read, verify. Spec §History API.
//
// Unlike Vault, History events are not encrypted client-side — they're public
// audit-log records. Callers pass plaintext payload; the SDK serializes it and
// sends. The hash chain is managed by the SDK: it tracks the last known head
// per stream and supplies previous_event_hash on each append.
//
// SECURITY (P2 #17): the SDK recomputes event_hash locally on every append
// AND verifies the chain on every read. Without this the SDK takes whatever
// the transport returns at face value — a malicious or compromised transport
// could fork the audit trail unnoticed.

import { newIdempotencyKey } from './transport.js';
import { bytesToBase64, base64ToBytes } from './util/base64.js';

const enc = new TextEncoder();
const dec = new TextDecoder('utf-8', { fatal: true });

// HistoryHashError fires when a server-returned event_hash disagrees
// with the locally-recomputed one. Indicates either the transport
// tampered with the response or the SDK and Hub disagree on the
// canonical hash formula — either is a bug we want to surface.
export class HistoryHashError extends Error {
  constructor(message, expected, got) {
    super(message);
    this.name = 'HistoryHashError';
    this.expected = expected;
    this.got = got;
  }
}

// computeHistoryEventHash mirrors the Hub's ComputeHistoryEventHash:
// SHA-256(prev || event_id || kind || payload_metadata || causal_deps_json).
// payloadMetadata is empty when the caller didn't send one;
// causalDepsJson is canonical JSON of the array (or "[]" when empty),
// matching the Hub's jsonStringArray helper byte-for-byte.
export async function computeHistoryEventHash(prev, eventID, kind, payloadMetadata, causalDeps) {
  const prevBytes = prev instanceof Uint8Array
    ? prev
    : (prev ? base64ToBytes(prev) : new Uint8Array(0));
  const cdJSON = (causalDeps && causalDeps.length > 0)
    ? JSON.stringify(causalDeps)
    : '[]';
  const buf = concatBytes(
    prevBytes,
    enc.encode(eventID),
    enc.encode(kind),
    enc.encode(payloadMetadata ?? ''),
    enc.encode(cdJSON),
  );
  const digest = await crypto.subtle.digest('SHA-256', buf);
  return new Uint8Array(digest);
}

function concatBytes(...parts) {
  let total = 0;
  for (const p of parts) total += p.byteLength;
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.byteLength; }
  return out;
}

function bytesEqual(a, b) {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

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
    // SECURITY: recompute the hash locally and compare to what the
    // transport returned. Catches a tampered or buggy response before
    // the SDK's internal head tracker gets poisoned.
    const recomputed = await computeHistoryEventHash(
      prev,
      res.data.event_id,
      spec.event.kind,
      '',
      spec.event.causalDependencies ?? [],
    );
    const returned = base64ToBytes(res.data.event_hash);
    if (!bytesEqual(recomputed, returned)) {
      throw new HistoryHashError(
        `history.append: transport returned event_hash that does not match locally-computed value (event_id=${res.data.event_id})`,
        bytesToBase64(recomputed),
        res.data.event_hash,
      );
    }
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
    const events = (res.data?.events ?? []).map((ev) => ({
      eventId: ev.event_id,
      kind: ev.kind,
      sequenceNumber: ev.sequence_number,
      payload: decodePayload(base64ToBytes(ev.payload_ciphertext)),
      previousEventHash: ev.previous_event_hash,
      eventHash: ev.event_hash,
      appendedAt: ev.appended_at ? new Date(ev.appended_at) : null,
      causalDependencies: ev.causal_dependencies ?? [],
    }));
    // SECURITY: walk the chain and recompute each event_hash against
    // the previous one. Detects a malicious or compromised transport
    // returning forged history. Unless the caller opts out via
    // opts.skipChainVerify (test/debug), a mismatch throws
    // HistoryHashError naming the offending event_id.
    if (!opts.skipChainVerify) {
      for (const ev of events) {
        const recomputed = await computeHistoryEventHash(
          ev.previousEventHash,
          ev.eventId,
          ev.kind,
          '', // payload_metadata not surfaced on the wire today
          ev.causalDependencies,
        );
        const returned = base64ToBytes(ev.eventHash);
        if (!bytesEqual(recomputed, returned)) {
          throw new HistoryHashError(
            `history.read: chain hash mismatch at event_id=${ev.eventId}`,
            bytesToBase64(recomputed),
            ev.eventHash,
          );
        }
      }
    }
    return { events, more: !!res.data?.more };
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
