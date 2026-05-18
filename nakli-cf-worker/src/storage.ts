// KV + R2 storage layout per cf-worker-spec-001-v1.1.md §"Storage layout".
//
// All wire-side data structures pass through here so the rest of the Worker
// can be backend-agnostic.

import type { Env } from './env.js';
import { ulid } from 'ulidx';
import { sha256Hex } from './hash.js';

// ----- Principal state -----

export interface PrincipalRecord {
  principal_id: string;
  principal_type: 'human' | 'agent' | 'device';
  public_key?: string; // base64
  parent_principal_id?: string;
  display_name?: string;
  created_at: string;
  retired_at?: string;
  retirement_event_id?: string;
}

export async function getPrincipal(env: Env, id: string): Promise<PrincipalRecord | null> {
  const raw = await env.STATE.get('principal:' + stripFabricSuffix(id));
  return raw ? JSON.parse(raw) : null;
}

export async function upsertPrincipal(env: Env, p: PrincipalRecord): Promise<void> {
  await env.STATE.put('principal:' + p.principal_id, JSON.stringify(p));
}

export async function retirePrincipal(env: Env, id: string, retirementEventId: string): Promise<void> {
  const cur = (await getPrincipal(env, id)) ?? {
    principal_id: stripFabricSuffix(id),
    principal_type: 'agent',
    created_at: new Date().toISOString(),
  };
  cur.retired_at = new Date().toISOString();
  cur.retirement_event_id = retirementEventId;
  await upsertPrincipal(env, cur);
}

function stripFabricSuffix(id: string): string {
  const at = id.indexOf('@');
  return at === -1 ? id : id.slice(0, at);
}

// ----- Stream state -----

export const HistoryNamespace = '__history__';

export interface StreamRecord {
  namespace: string;
  stream_id: string;
  stream_type: 'vault' | 'history';
  created_at: string;
  head_event_id: string;
  head_event_hash: string; // base64
  event_count: number;
}

const streamKey = (ns: string, sid: string) => `stream:${ns}:${sid}`;
const streamIndexKey = (ns: string) => `stream_index:${ns}`;
const eventIndexKey = (ns: string, sid: string, seq: number) =>
  `event_index:${ns}:${sid}:${seq.toString().padStart(12, '0')}`;

export async function getStream(env: Env, ns: string, sid: string): Promise<StreamRecord | null> {
  const raw = await env.STATE.get(streamKey(ns, sid));
  return raw ? JSON.parse(raw) : null;
}

async function putStream(env: Env, s: StreamRecord): Promise<void> {
  await env.STATE.put(streamKey(s.namespace, s.stream_id), JSON.stringify(s));
  // Add to namespace index (best-effort; KV reads are eventually consistent).
  const idxRaw = await env.STATE.get(streamIndexKey(s.namespace));
  const idx: string[] = idxRaw ? JSON.parse(idxRaw) : [];
  if (!idx.includes(s.stream_id)) {
    idx.push(s.stream_id);
    await env.STATE.put(streamIndexKey(s.namespace), JSON.stringify(idx));
  }
}

export async function listStreams(env: Env, ns: string): Promise<StreamRecord[]> {
  const idxRaw = await env.STATE.get(streamIndexKey(ns));
  const ids: string[] = idxRaw ? JSON.parse(idxRaw) : [];
  const out: StreamRecord[] = [];
  for (const id of ids) {
    const s = await getStream(env, ns, id);
    if (s) out.push(s);
  }
  return out;
}

// ----- Events -----

export interface EventRecord {
  event_id: string;
  namespace: string;
  stream_id: string;
  kind: string;
  sequence_number: number;
  blob_key: string;
  payload_metadata?: any;
  causal_dependencies?: string[];
  vector_clock?: Record<string, number>;
  previous_event_hash?: string;
  event_hash?: string;
  appended_at: string;
  appended_by_principal: string;
  appended_by_grant_id: string;
}

function blobKey(ns: string, sid: string, eventId: string): string {
  return `blobs/${ns}/${sid}/${eventId}.bin`;
}

export interface AppendInput {
  namespace: string;
  stream_id: string;
  stream_type: 'vault' | 'history';
  kind: string;
  payload_ciphertext: Uint8Array;
  payload_metadata?: any;
  causal_dependencies?: string[];
  vector_clock?: Record<string, number>;
  appended_by_principal: string;
  appended_by_grant_id: string;
  previous_event_hash?: Uint8Array | null; // history only
}

export interface AppendResult {
  event_id: string;
  sequence_number: number;
  event_hash?: string; // base64; populated for history
}

export class HistoryConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'HistoryConflictError';
  }
}

export async function appendEvent(env: Env, in_: AppendInput): Promise<AppendResult> {
  const eventId = ulid();
  const stream = (await getStream(env, in_.namespace, in_.stream_id)) ?? {
    namespace: in_.namespace,
    stream_id: in_.stream_id,
    stream_type: in_.stream_type,
    created_at: new Date().toISOString(),
    head_event_id: '',
    head_event_hash: '',
    event_count: 0,
  };
  let eventHash: string | undefined;
  if (in_.stream_type === 'history') {
    // Hash chain: hash = SHA-256(previous_hash || ciphertext || event_id).
    const prev = stream.head_event_hash ? base64ToBytes(stream.head_event_hash) : new Uint8Array(0);
    const supplied = in_.previous_event_hash ?? new Uint8Array(0);
    if (!bytesEqual(prev, supplied)) {
      throw new HistoryConflictError('previous_event_hash does not match current stream head');
    }
    const buf = new Uint8Array(prev.length + in_.payload_ciphertext.length + eventId.length);
    buf.set(prev, 0);
    buf.set(in_.payload_ciphertext, prev.length);
    buf.set(new TextEncoder().encode(eventId), prev.length + in_.payload_ciphertext.length);
    const h = await sha256Hex(buf);
    const bytes = hexToBytes(h);
    eventHash = bytesToBase64(bytes);
  }

  const seq = stream.event_count + 1;
  const key = blobKey(in_.namespace, in_.stream_id, eventId);
  await env.BLOBS.put(key, in_.payload_ciphertext, {
    httpMetadata: { contentType: 'application/octet-stream' },
    customMetadata: {
      'x-fabric-event-kind': in_.kind,
      'x-fabric-appended-at': new Date().toISOString(),
      'x-fabric-appended-by': in_.appended_by_principal,
      'x-fabric-sequence-number': seq.toString(),
    },
  });

  const ev: EventRecord = {
    event_id: eventId,
    namespace: in_.namespace,
    stream_id: in_.stream_id,
    kind: in_.kind,
    sequence_number: seq,
    blob_key: key,
    payload_metadata: in_.payload_metadata,
    causal_dependencies: in_.causal_dependencies ?? [],
    vector_clock: in_.vector_clock ?? {},
    previous_event_hash: in_.previous_event_hash && in_.previous_event_hash.length
      ? bytesToBase64(in_.previous_event_hash) : '',
    event_hash: eventHash,
    appended_at: new Date().toISOString(),
    appended_by_principal: in_.appended_by_principal,
    appended_by_grant_id: in_.appended_by_grant_id,
  };
  await env.STATE.put(eventIndexKey(in_.namespace, in_.stream_id, seq), JSON.stringify(ev));

  stream.head_event_id = eventId;
  stream.head_event_hash = eventHash ?? '';
  stream.event_count = seq;
  await putStream(env, stream);

  return { event_id: eventId, sequence_number: seq, event_hash: eventHash };
}

export interface ReadOptions {
  sinceEventId?: string;
  limit?: number;
}

export async function readStream(env: Env, ns: string, sid: string, opts: ReadOptions = {}): Promise<{ events: EventRecord[]; more: boolean }> {
  const stream = await getStream(env, ns, sid);
  if (!stream) return { events: [], more: false };
  const limit = opts.limit && opts.limit > 0 ? Math.min(opts.limit, 1000) : 100;
  // Find starting sequence: if sinceEventId, scan the index forward looking
  // for it (events are addressed by sequence; we don't store an
  // event_id→seq reverse index in KV, so we walk).
  let startSeq = 1;
  if (opts.sinceEventId) {
    for (let s = 1; s <= stream.event_count; s++) {
      const raw = await env.STATE.get(eventIndexKey(ns, sid, s));
      if (!raw) continue;
      const ev: EventRecord = JSON.parse(raw);
      if (ev.event_id === opts.sinceEventId) { startSeq = s + 1; break; }
    }
  }
  const out: EventRecord[] = [];
  for (let s = startSeq; s <= stream.event_count && out.length < limit; s++) {
    const raw = await env.STATE.get(eventIndexKey(ns, sid, s));
    if (!raw) continue;
    const ev: EventRecord = JSON.parse(raw);
    // Hydrate payload from R2.
    const obj = await env.BLOBS.get(ev.blob_key);
    if (obj) {
      const buf = new Uint8Array(await obj.arrayBuffer());
      (ev as any).payload_ciphertext = bytesToBase64(buf);
    } else {
      (ev as any).payload_ciphertext = '';
    }
    out.push(ev);
  }
  return { events: out, more: startSeq + out.length <= stream.event_count };
}

export async function verifyHistory(env: Env, sid: string): Promise<{ verified: boolean; length: number; head_hash: string; broken_at_event_id?: string }> {
  const stream = await getStream(env, HistoryNamespace, sid);
  if (!stream) return { verified: true, length: 0, head_hash: '' };
  let prev: Uint8Array = new Uint8Array(0);
  for (let s = 1; s <= stream.event_count; s++) {
    const raw = await env.STATE.get(eventIndexKey(HistoryNamespace, sid, s));
    if (!raw) return { verified: false, length: s - 1, head_hash: bytesToBase64(prev), broken_at_event_id: '' };
    const ev: EventRecord = JSON.parse(raw);
    const obj = await env.BLOBS.get(ev.blob_key);
    if (!obj) return { verified: false, length: s - 1, head_hash: bytesToBase64(prev), broken_at_event_id: ev.event_id };
    const cipher = new Uint8Array(await obj.arrayBuffer());
    const buf = new Uint8Array(prev.length + cipher.length + ev.event_id.length);
    buf.set(prev, 0);
    buf.set(cipher, prev.length);
    buf.set(new TextEncoder().encode(ev.event_id), prev.length + cipher.length);
    const expectedHex = await sha256Hex(buf);
    const expected = hexToBytes(expectedHex);
    const stored = ev.event_hash ? base64ToBytes(ev.event_hash) : new Uint8Array(0);
    if (!bytesEqual(expected, stored)) {
      return { verified: false, length: s - 1, head_hash: bytesToBase64(prev), broken_at_event_id: ev.event_id };
    }
    prev = expected;
  }
  return { verified: true, length: stream.event_count, head_hash: bytesToBase64(prev) };
}

// ----- Idempotency -----

export interface IdempotencyRecord {
  payload_hash: string; // hex
  response_status: number;
  response_body_b64: string;
  expires_at: string;
}

export async function idempotencyLookup(env: Env, key: string, grantId: string, payloadHashHex: string): Promise<{ replay: boolean; status?: number; bodyBytes?: Uint8Array; conflict?: boolean }> {
  const k = `idempotency:${grantId}:${key}`;
  const raw = await env.STATE.get(k);
  if (!raw) return { replay: false };
  const rec: IdempotencyRecord = JSON.parse(raw);
  if (rec.payload_hash !== payloadHashHex) return { replay: false, conflict: true };
  return {
    replay: true,
    status: rec.response_status,
    bodyBytes: base64ToBytes(rec.response_body_b64),
  };
}

export async function idempotencyPut(env: Env, key: string, grantId: string, payloadHashHex: string, status: number, bodyBytes: Uint8Array, retentionSeconds: number): Promise<void> {
  const k = `idempotency:${grantId}:${key}`;
  const rec: IdempotencyRecord = {
    payload_hash: payloadHashHex,
    response_status: status,
    response_body_b64: bytesToBase64(bodyBytes),
    expires_at: new Date(Date.now() + retentionSeconds * 1000).toISOString(),
  };
  await env.STATE.put(k, JSON.stringify(rec), {
    expirationTtl: Math.max(retentionSeconds, 60),
  });
}

// ----- Grant revocation state -----

export interface GrantRecord {
  grant_id: string;
  issued_by_principal: string;
  recipient_principal: string;
  parent_grant_id?: string;
  revoked_at?: string;
  revocation_event_id?: string;
}

export async function getGrant(env: Env, grantId: string): Promise<GrantRecord | null> {
  const raw = await env.STATE.get('grant:' + grantId);
  return raw ? JSON.parse(raw) : null;
}

export async function markGrantRevoked(env: Env, grantId: string, revocationEventId: string): Promise<void> {
  const cur = (await getGrant(env, grantId)) ?? {
    grant_id: grantId,
    issued_by_principal: '',
    recipient_principal: '',
  };
  cur.revoked_at = new Date().toISOString();
  cur.revocation_event_id = revocationEventId;
  await env.STATE.put('grant:' + grantId, JSON.stringify(cur));
}

export async function isGrantRevoked(env: Env, grantId: string): Promise<boolean> {
  const g = await getGrant(env, grantId);
  return !!g?.revoked_at;
}

// ----- Pending bridge calls -----

export interface PendingRecord {
  pending_id: string;
  grant_id: string;
  adapter: string;
  operation: string;
  params: any;
  requested_by_principal: string;
  requested_at: string;
  approved_at?: string;
}

export async function insertPending(env: Env, rec: PendingRecord): Promise<void> {
  await env.STATE.put('pending:' + rec.pending_id, JSON.stringify(rec));
}
export async function getPending(env: Env, id: string): Promise<PendingRecord | null> {
  const raw = await env.STATE.get('pending:' + id);
  return raw ? JSON.parse(raw) : null;
}

// ----- Pairing tokens -----

export interface PairingRecord {
  token: string;
  numeric_code: string;
  initiated_by_principal: string;
  initiated_at: string;
  expires_at: string;
  completed_at?: string;
  completed_by_device?: string;
}

export async function putPairing(env: Env, rec: PairingRecord, ttlSec: number): Promise<void> {
  await env.STATE.put('pairing:' + rec.token, JSON.stringify(rec), { expirationTtl: ttlSec });
  await env.STATE.put('pairing_code:' + rec.numeric_code, rec.token, { expirationTtl: ttlSec });
}
export async function getPairingByToken(env: Env, token: string): Promise<PairingRecord | null> {
  const raw = await env.STATE.get('pairing:' + token);
  return raw ? JSON.parse(raw) : null;
}
export async function markPairingCompleted(env: Env, rec: PairingRecord): Promise<void> {
  await env.STATE.put('pairing:' + rec.token, JSON.stringify(rec));
}

// ----- CRATE-PAIR tokens (Unit C) -----
//
// Mirrors the Hub-side `crate_pairing_tokens` table (see
// nakli-hub/internal/storage/crate_pairing.go) but lives in KV instead of
// SQLite. KV is eventually consistent — redemption uses a read-then-
// conditional-write pattern; the race window is documented as a v1.0
// limitation (see plan/Unit-C-notes.md).
//
// Key layout:
//   crate-pairing:{secret}        — primary lookup; value = CratePairingTokenRecord JSON
//
// TTL is set to (expires_at - now) at put time so KV auto-evicts stale
// rows. Redemption / cancellation re-puts with an extended TTL so audit
// records survive a bit past the original token expiry.

export interface CratePairingTokenRecord {
  secret: string;
  payload_json: string;
  bucket_id: string;
  identity_pubkey: string;
  transport_endpoint: string;
  transport_type: string;
  issued_at: string;
  expires_at: string;
  redeemed_at?: string;
  redeemed_by_daemon_pubkey?: string;
  daemon_fingerprint?: string;
  issued_capability_id?: string;
  cancelled_at?: string;
  created_at: string;
}

const cpKey = (secret: string) => 'crate-pairing:' + secret;

export async function putCratePairingToken(env: Env, rec: CratePairingTokenRecord, ttlSec: number): Promise<void> {
  const opts: KVNamespacePutOptions = {};
  // KV's minimum expirationTtl is 60s.
  if (ttlSec >= 60) opts.expirationTtl = ttlSec;
  await env.STATE.put(cpKey(rec.secret), JSON.stringify(rec), opts);
}

export async function getCratePairingToken(env: Env, secret: string): Promise<CratePairingTokenRecord | null> {
  const raw = await env.STATE.get(cpKey(secret));
  return raw ? JSON.parse(raw) : null;
}

// updateCratePairingToken overwrites the existing row, preserving its
// remaining TTL (KV doesn't support partial updates).
export async function updateCratePairingToken(env: Env, rec: CratePairingTokenRecord): Promise<void> {
  const expires = new Date(rec.expires_at).getTime();
  // Keep the audit record for 24h after expiry so subsequent replays still
  // surface "token already redeemed" instead of "token not found".
  const auditTtl = Math.max(60, Math.floor((expires + 86_400_000 - Date.now()) / 1000));
  await env.STATE.put(cpKey(rec.secret), JSON.stringify(rec), { expirationTtl: auditTtl });
}

// ----- Helpers -----

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
function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}
function hexToBytes(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}
