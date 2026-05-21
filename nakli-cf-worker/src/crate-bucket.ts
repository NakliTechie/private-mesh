// crate-agent M3 piece 2 — cf-worker parity for the Hub bucket-proxy.
//
// Mirrors nakli-hub/internal/{crate,server,storage}/handlers_crate_bucket*.go.
// The daemon's capability authenticates against either transport (Hub OR
// cf-worker) without code changes: same 7 endpoints, same response shapes,
// same error envelope.
//
// Differences from the Hub port (all justified by the cf-worker runtime):
//
//   * Storage: KV (key=`crate-bucket:{bucket_id}`) instead of SQLite. KV is
//     eventually consistent; the same caveat documented in Unit C's
//     cf-worker parity applies — race windows exist on concurrent edits.
//     For crate-bucket the only mutation after Register is TouchLastUsed,
//     which is intentionally best-effort.
//
//   * AEAD: AES-GCM-256 (via SubtleCrypto) instead of XChaCha20-Poly1305.
//     Workers' built-in WebCrypto doesn't expose XChaCha; AES-GCM is the
//     canonical choice for at-rest encryption on Workers. Same 256-bit key
//     length; 12-byte nonce instead of 24 (sufficient given we generate
//     a fresh nonce per row + use HKDF-derived key, so birthday-bound
//     collision risk is negligible).
//
//   * sig-v4: SubtleCrypto-backed HMAC-SHA256 chain (TypeScript port of
//     crate/lib/sigv4.js — same algorithm as the Hub's Go port, same
//     reference vector). The wire is byte-identical to the Hub.

import type { Env } from './env';
import { errorResponse, successResponse, type ErrorCode } from './envelope';
import { base64ToBytes, bytesToBase64 } from './macaroon';

// --- KV storage shape -------------------------------------------------------

// Stored at key `crate-bucket:{bucket_id}`. Value is a JSON blob; the
// secret access key is sealed (AES-GCM ciphertext, base64).
export interface CrateBucketRecord {
  bucket_id: string;
  provider: 'r2' | 'b2' | 'hetzner' | 'aws-s3';
  account_id: string; // R2 only; "" for others
  region: string;
  bucket_name: string;
  endpoint_url: string;
  access_key_id: string;
  secret_access_key_sealed: string; // base64 ciphertext
  nonce: string;                    // base64; 12 bytes for AES-GCM
  registered_by_principal: string;
  created_at: string;               // RFC3339
  last_used_at?: string;            // RFC3339
}

function kvKey(bucketID: string): string {
  return `crate-bucket:${bucketID}`;
}

export async function putCrateBucket(env: Env, rec: CrateBucketRecord): Promise<void> {
  await env.STATE.put(kvKey(rec.bucket_id), JSON.stringify(rec));
}

export async function getCrateBucket(env: Env, bucketID: string): Promise<CrateBucketRecord | null> {
  const s = await env.STATE.get(kvKey(bucketID));
  if (!s) return null;
  try {
    return JSON.parse(s) as CrateBucketRecord;
  } catch {
    return null;
  }
}

export async function touchCrateBucketLastUsed(env: Env, bucketID: string): Promise<void> {
  const rec = await getCrateBucket(env, bucketID);
  if (!rec) return;
  rec.last_used_at = new Date().toISOString();
  await putCrateBucket(env, rec);
}

// --- Encryption-at-rest (HKDF + AES-GCM) ------------------------------------

const CREDS_KEY_SALT = new TextEncoder().encode('crate-buckets');
const CREDS_KEY_INFO = new TextEncoder().encode('v1');

// deriveBucketCredsKey returns the 32-byte AES-GCM key derived from the
// Worker's macaroon root key. Same construction as the Hub side; rotating
// the macaroon root also rotates this.
export async function deriveBucketCredsKey(macaroonRootKeyB64: string): Promise<CryptoKey> {
  const rootBytes = base64ToBytes(macaroonRootKeyB64);
  if (rootBytes.length !== 32) {
    throw new Error(`macaroon root key length ${rootBytes.length}, want 32`);
  }
  const ikm = await crypto.subtle.importKey(
    'raw', rootBytes as BufferSource, { name: 'HKDF' }, false, ['deriveKey'],
  );
  return crypto.subtle.deriveKey(
    { name: 'HKDF', hash: 'SHA-256', salt: CREDS_KEY_SALT as BufferSource, info: CREDS_KEY_INFO as BufferSource },
    ikm,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt'],
  );
}

// sealSecret encrypts plaintext with credsKey + a fresh random 12-byte nonce.
// bucketID is bound as AAD — moving sealed bytes from row A to row B fails.
export async function sealSecret(
  credsKey: CryptoKey,
  plaintext: Uint8Array,
  bucketID: string,
): Promise<{ ciphertextB64: string; nonceB64: string }> {
  if (plaintext.length === 0) throw new Error('crate: refusing to seal empty plaintext');
  if (!bucketID) throw new Error('crate: SealSecret: empty bucketID (AAD required)');
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const aad = new TextEncoder().encode(bucketID);
  const ct = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: aad as BufferSource },
    credsKey,
    plaintext as BufferSource,
  );
  return { ciphertextB64: bytesToBase64(new Uint8Array(ct)), nonceB64: bytesToBase64(nonce) };
}

export async function openSecret(
  credsKey: CryptoKey,
  ciphertextB64: string,
  nonceB64: string,
  bucketID: string,
): Promise<Uint8Array> {
  if (!bucketID) throw new Error('crate: OpenSecret: empty bucketID (AAD required)');
  const ct = base64ToBytes(ciphertextB64);
  const nonce = base64ToBytes(nonceB64);
  const aad = new TextEncoder().encode(bucketID);
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: aad as BufferSource },
    credsKey,
    ct as BufferSource,
  );
  return new Uint8Array(plain);
}

// --- Endpoint builders ------------------------------------------------------

export function endpointR2(accountID: string, bucket: string): string {
  return `https://${accountID}.r2.cloudflarestorage.com/${encodeURIComponent(bucket)}/`;
}
export function endpointHetzner(datacenter: string, bucket: string): string {
  return `https://${encodeURIComponent(bucket)}.${datacenter}.your-objectstorage.com/`;
}
export function endpointB2(region: string, bucket: string): string {
  return `https://${encodeURIComponent(bucket)}.s3.${region}.backblazeb2.com/`;
}
export function endpointAWS(region: string, bucket: string): string {
  return `https://${encodeURIComponent(bucket)}.s3.${region}.amazonaws.com/`;
}

export function endpointForProvider(
  provider: string,
  accountID: string,
  region: string,
  bucket: string,
): string | null {
  switch (provider.toLowerCase()) {
    case 'r2': return endpointR2(accountID, bucket);
    case 'hetzner': return endpointHetzner(region, bucket);
    case 'b2': return endpointB2(region, bucket);
    case 'aws-s3':
    case 'aws': return endpointAWS(region, bucket);
    default: return null;
  }
}

// --- AWS Signature Version 4 ------------------------------------------------
//
// TypeScript port of crate/lib/sigv4.js. Same canonical-request shape, same
// 4-iteration HMAC signing-key chain, same empty-body-SHA256 default. Verified
// against the AWS-published reference vector ("GET Object" example).

const SIGV4_ALG = 'AWS4-HMAC-SHA256';
const EMPTY_BODY_SHA256 = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
export const UNSIGNED_PAYLOAD = 'UNSIGNED-PAYLOAD';

function toHex(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let s = '';
  for (let i = 0; i < bytes.length; i++) s += bytes[i].toString(16).padStart(2, '0');
  return s;
}

async function sha256Hex(data: string | Uint8Array): Promise<string> {
  const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data;
  const h = await crypto.subtle.digest('SHA-256', bytes as BufferSource);
  return toHex(h);
}

async function hmac(keyBytes: Uint8Array, data: string | Uint8Array): Promise<Uint8Array> {
  const k = await crypto.subtle.importKey(
    'raw', keyBytes as BufferSource, { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'],
  );
  const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data;
  const sig = await crypto.subtle.sign('HMAC', k, bytes as BufferSource);
  return new Uint8Array(sig);
}

function uriEncodeSegment(s: string): string {
  return s.replace(/[^A-Za-z0-9_\-~.]/g, (c) => {
    const bytes = new TextEncoder().encode(c);
    let out = '';
    for (const b of bytes) out += '%' + b.toString(16).toUpperCase().padStart(2, '0');
    return out;
  });
}

function canonicalPath(p: string): string {
  if (!p || p === '') return '/';
  return p.split('/').map(uriEncodeSegment).join('/');
}

function canonicalQuery(sp: URLSearchParams): string {
  const pairs: [string, string][] = [];
  for (const [k, v] of sp.entries()) pairs.push([uriEncodeSegment(k), uriEncodeSegment(v ?? '')]);
  pairs.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : a[1] < b[1] ? -1 : 1));
  return pairs.map(([k, v]) => `${k}=${v}`).join('&');
}

function canonicalHeaders(h: Record<string, string>): { canonical: string; signed: string } {
  const lower: [string, string][] = Object.entries(h).map(([k, v]) =>
    [k.toLowerCase(), String(v).trim().replace(/\s+/g, ' ')]);
  lower.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  const canonical = lower.map(([k, v]) => `${k}:${v}\n`).join('');
  const signed = lower.map(([k]) => k).join(';');
  return { canonical, signed };
}

function isoDateTime(d: Date): string {
  return d.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z');
}

export interface SignOpts {
  method: string;
  url: string;
  headers?: Record<string, string>;
  payloadHash?: string; // empty → empty-body SHA-256; UNSIGNED_PAYLOAD for streamed PUT
  region: string;
  service?: string;
  accessKey: string;
  secretKey: string;
  date?: Date;
}

export async function signRequest(opts: SignOpts): Promise<Record<string, string>> {
  const {
    method = 'GET', url, headers = {}, payloadHash, region,
    service = 's3', accessKey, secretKey, date = new Date(),
  } = opts;
  if (!url) throw new Error('signRequest: url is required');
  if (!region) throw new Error('signRequest: region is required');
  if (!accessKey || !secretKey) throw new Error('signRequest: accessKey + secretKey required');

  const u = new URL(url);
  const amzDate = isoDateTime(date);
  const dateStamp = amzDate.slice(0, 8);

  const pHash = payloadHash && payloadHash.length > 0 ? payloadHash : EMPTY_BODY_SHA256;
  const merged: Record<string, string> = {
    ...headers,
    host: u.host,
    'x-amz-date': amzDate,
    'x-amz-content-sha256': pHash,
  };

  const { canonical: canonHeaders, signed: signedHeaders } = canonicalHeaders(merged);
  const canonRequest = [
    method.toUpperCase(),
    canonicalPath(u.pathname),
    canonicalQuery(u.searchParams),
    canonHeaders,
    signedHeaders,
    pHash,
  ].join('\n');

  const scope = `${dateStamp}/${region}/${service}/aws4_request`;
  const stringToSign = [SIGV4_ALG, amzDate, scope, await sha256Hex(canonRequest)].join('\n');

  const kDate = await hmac(new TextEncoder().encode('AWS4' + secretKey), dateStamp);
  const kRegion = await hmac(kDate, region);
  const kService = await hmac(kRegion, service);
  const kSigning = await hmac(kService, 'aws4_request');
  const signature = toHex(await hmac(kSigning, stringToSign));

  return {
    ...merged,
    Authorization: `${SIGV4_ALG} Credential=${accessKey}/${scope}, SignedHeaders=${signedHeaders}, Signature=${signature}`,
  };
}

// --- Handlers ---------------------------------------------------------------

interface RegisterRequest {
  provider?: string;
  account_id?: string;
  region?: string;
  bucket_name?: string;
  access_key?: string;
  secret_key?: string;
  endpoint_url?: string;
}

interface MetadataResp {
  bucket_id: string;
  provider: string;
  region: string;
  bucket_name: string;
  endpoint_url: string;
  created_at: string;
  last_used_at?: string;
}

const ALLOWED_PROVIDERS = new Set(['r2', 'hetzner', 'b2', 'aws-s3']);

// generateULID returns a Crockford-base32 ULID. Uses 48-bit timestamp + 80
// random bits; matches the Go `oklog/ulid/v2` shape.
function generateULID(): string {
  const ALPHA = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
  const timeBytes = new Uint8Array(6);
  let ts = Date.now();
  for (let i = 5; i >= 0; i--) { timeBytes[i] = ts & 0xff; ts = Math.floor(ts / 256); }
  const randBytes = crypto.getRandomValues(new Uint8Array(10));
  const bits: number[] = [];
  for (const b of timeBytes) { bits.push((b >> 4) & 0x0f, b & 0x0f); }
  for (const b of randBytes) { bits.push((b >> 4) & 0x0f, b & 0x0f); }
  // Above gives us 4-bit groups; ULID wants 5-bit groups for 26 chars.
  // Easier: encode all 16 bytes as a single big-uint into base32 via bit ops.
  const all = new Uint8Array(16);
  all.set(timeBytes, 0);
  all.set(randBytes, 6);
  let s = '';
  let acc = 0;
  let accBits = 0;
  for (const b of all) {
    acc = (acc << 8) | b;
    accBits += 8;
    while (accBits >= 5) {
      accBits -= 5;
      s += ALPHA[(acc >> accBits) & 0x1f];
    }
  }
  if (accBits > 0) s += ALPHA[(acc << (5 - accBits)) & 0x1f];
  return s.slice(0, 26);
}

export async function handleCrateBucketRegister(req: Request, env: Env, principalID: string): Promise<Response> {
  let body: RegisterRequest;
  try {
    body = await req.json() as RegisterRequest;
  } catch {
    return errorResponse('bad_request', 'request body is not valid JSON', 400);
  }
  const provider = (body.provider ?? '').toLowerCase().trim();
  if (!ALLOWED_PROVIDERS.has(provider)) {
    return errorResponse('bad_request', 'unsupported provider; allowed: r2, hetzner, b2, aws-s3', 400);
  }
  if (!body.bucket_name || !body.access_key || !body.secret_key) {
    return errorResponse('bad_request', 'bucket_name, access_key, secret_key are required', 400);
  }
  if (provider === 'r2' && !body.account_id) {
    return errorResponse('bad_request', 'account_id is required for provider=r2', 400);
  }
  let region = body.region ?? '';
  if (!region) {
    if (provider === 'r2') region = 'auto';
    else return errorResponse('bad_request', `region is required for provider=${provider}`, 400);
  }
  let endpoint = body.endpoint_url ?? '';
  if (!endpoint) {
    const built = endpointForProvider(provider, body.account_id ?? '', region, body.bucket_name);
    if (!built) return errorResponse('bad_request', `could not build endpoint URL for provider=${provider}`, 400);
    endpoint = built;
  }
  const bucketID = 'bk_' + generateULID();
  let credsKey: CryptoKey;
  try {
    credsKey = await deriveBucketCredsKey(env.MACAROON_ROOT_KEY);
  } catch (e: any) {
    return errorResponse('unavailable', 'key derivation failed', 500, true);
  }
  let sealed: { ciphertextB64: string; nonceB64: string };
  try {
    sealed = await sealSecret(credsKey, new TextEncoder().encode(body.secret_key), bucketID);
  } catch (e: any) {
    return errorResponse('unavailable', 'seal failed', 500, true);
  }
  const rec: CrateBucketRecord = {
    bucket_id: bucketID,
    provider: provider as CrateBucketRecord['provider'],
    account_id: body.account_id ?? '',
    region,
    bucket_name: body.bucket_name,
    endpoint_url: endpoint,
    access_key_id: body.access_key,
    secret_access_key_sealed: sealed.ciphertextB64,
    nonce: sealed.nonceB64,
    registered_by_principal: principalID,
    created_at: new Date().toISOString(),
  };
  try {
    await putCrateBucket(env, rec);
  } catch (e: any) {
    return errorResponse('unavailable', 'could not store bucket registration', 500, true);
  }
  return successResponse({ bucket_id: bucketID }, null, 201);
}

export async function handleCrateBucketMetadata(bucketID: string, env: Env): Promise<Response> {
  if (!bucketID) return errorResponse('bad_request', 'bucket_id is required', 400);
  const rec = await getCrateBucket(env, bucketID);
  if (!rec) return errorResponse('not_found', 'no bucket matches that bucket_id', 404);
  const resp: MetadataResp = {
    bucket_id: rec.bucket_id,
    provider: rec.provider,
    region: rec.region,
    bucket_name: rec.bucket_name,
    endpoint_url: rec.endpoint_url,
    created_at: rec.created_at,
  };
  if (rec.last_used_at) resp.last_used_at = rec.last_used_at;
  return successResponse(resp);
}

// handleCrateBucketList walks every crate-bucket:* KV key and returns the
// buckets whose registered_by_principal matches the caller. KV's list()
// is paginated; we paginate via cursor until exhausted.
export async function handleCrateBucketList(env: Env, principalID: string): Promise<Response> {
  const buckets: MetadataResp[] = [];
  let cursor: string | undefined = undefined;
  // Hard cap iterations so a runaway never hangs the request.
  for (let i = 0; i < 50; i++) {
    const opts: { prefix: string; cursor?: string } = { prefix: 'crate-bucket:' };
    if (cursor) opts.cursor = cursor;
    const page = await env.STATE.list(opts);
    for (const key of page.keys) {
      const raw = await env.STATE.get(key.name);
      if (!raw) continue;
      let rec: CrateBucketRecord;
      try { rec = JSON.parse(raw) as CrateBucketRecord; } catch { continue; }
      if (rec.registered_by_principal !== principalID) continue;
      const m: MetadataResp = {
        bucket_id: rec.bucket_id,
        provider: rec.provider,
        region: rec.region,
        bucket_name: rec.bucket_name,
        endpoint_url: rec.endpoint_url,
        created_at: rec.created_at,
      };
      if (rec.last_used_at) m.last_used_at = rec.last_used_at;
      buckets.push(m);
    }
    // KV's KVNamespaceListResult type is a discriminated union on
    // list_complete; access `cursor` only when not complete.
    if ('list_complete' in page && page.list_complete) break;
    if ('cursor' in page && typeof page.cursor === 'string') {
      cursor = page.cursor;
    } else {
      break;
    }
  }
  return successResponse({ buckets });
}

// loadAndDecrypt returns the record + plaintext secret access key.
async function loadAndDecrypt(env: Env, bucketID: string): Promise<{ rec: CrateBucketRecord; secret: string } | null> {
  const rec = await getCrateBucket(env, bucketID);
  if (!rec) return null;
  const credsKey = await deriveBucketCredsKey(env.MACAROON_ROOT_KEY);
  const plain = await openSecret(credsKey, rec.secret_access_key_sealed, rec.nonce, rec.bucket_id);
  return { rec, secret: new TextDecoder().decode(plain) };
}

// passThroughResponseHeaders: same allow-list as the Hub.
const PASSTHROUGH_RESP_HEADERS = [
  'Content-Type', 'Content-Length', 'Content-Range', 'Accept-Ranges',
  'ETag', 'Last-Modified',
  'x-amz-version-id', 'x-amz-request-id', 'x-amz-id-2', 'x-amz-meta-crate-iv',
];

export async function handleCrateObject(
  req: Request,
  env: Env,
  bucketID: string,
  objectPath: string,
): Promise<Response> {
  if (!bucketID) return errorResponse('bad_request', 'bucket_id is required', 400);
  if (!objectPath) return errorResponse('bad_request', 'object path is required', 400);

  const loaded = await loadAndDecrypt(env, bucketID);
  if (!loaded) return errorResponse('not_found', 'no bucket matches that bucket_id', 404);

  const { rec, secret } = loaded;
  const upstreamURL = rec.endpoint_url + objectPath;

  // Build headers we send upstream.
  const reqHeaders: Record<string, string> = {};
  if (req.method === 'PUT') {
    const ct = req.headers.get('Content-Type');
    if (ct) reqHeaders['Content-Type'] = ct;
  }
  // Propagate conditional headers on PUT/DELETE — R2 uses these for
  // concurrent-write safety (crate browser M6.x). Mirrors the Hub's
  // proxyToUpstream behaviour.
  if (req.method === 'PUT' || req.method === 'DELETE') {
    const ifMatch = req.headers.get('If-Match');
    if (ifMatch) reqHeaders['If-Match'] = ifMatch;
    const ifNoneMatch = req.headers.get('If-None-Match');
    if (ifNoneMatch) reqHeaders['If-None-Match'] = ifNoneMatch;
  }
  const payloadHash = req.method === 'PUT' ? UNSIGNED_PAYLOAD : ''; // empty = empty-body SHA-256

  let signed: Record<string, string>;
  try {
    signed = await signRequest({
      method: req.method, url: upstreamURL, headers: reqHeaders, payloadHash,
      region: rec.region, accessKey: rec.access_key_id, secretKey: secret,
    });
  } catch (e: any) {
    return errorResponse('unavailable', 'sig-v4 sign failed: ' + (e?.message ?? e), 500, true);
  }

  // Upstream fetch. For PUT we pass request.body (ReadableStream) directly —
  // Workers' fetch streams it through; no buffering.
  const upstreamInit: RequestInit = {
    method: req.method,
    headers: signed,
  };
  if (req.method === 'PUT') {
    upstreamInit.body = req.body;
  }
  let upstreamResp: Response;
  try {
    upstreamResp = await fetch(upstreamURL, upstreamInit);
  } catch (e: any) {
    return errorResponse('unavailable', 'upstream call failed: ' + (e?.message ?? e), 502, true);
  }

  // Best-effort: touch last_used_at on 2xx (don't await; let it happen async).
  if (upstreamResp.status >= 200 && upstreamResp.status < 300) {
    // Fire-and-forget; ignore errors.
    void touchCrateBucketLastUsed(env, rec.bucket_id).catch(() => {});
  }

  // Mirror safe response headers + status; pipe the body stream through.
  const outHeaders = new Headers();
  for (const h of PASSTHROUGH_RESP_HEADERS) {
    const v = upstreamResp.headers.get(h);
    if (v) outHeaders.set(h, v);
  }
  return new Response(upstreamResp.body, {
    status: upstreamResp.status,
    headers: outHeaders,
  });
}

export async function handleCrateList(req: Request, env: Env, bucketID: string): Promise<Response> {
  if (!bucketID) return errorResponse('bad_request', 'bucket_id is required', 400);
  const loaded = await loadAndDecrypt(env, bucketID);
  if (!loaded) return errorResponse('not_found', 'no bucket matches that bucket_id', 404);
  const { rec, secret } = loaded;

  // Translate our LIST query params to S3 v2 LIST.
  const reqURL = new URL(req.url);
  const params = new URLSearchParams();
  params.set('list-type', '2');
  const prefix = reqURL.searchParams.get('prefix');
  if (prefix) params.set('prefix', prefix);
  const cont = reqURL.searchParams.get('continuation_token');
  if (cont) params.set('continuation-token', cont);
  const maxKeys = reqURL.searchParams.get('max_keys');
  if (maxKeys) params.set('max-keys', maxKeys);

  const upstreamURL = rec.endpoint_url + '?' + params.toString();
  let signed: Record<string, string>;
  try {
    signed = await signRequest({
      method: 'GET', url: upstreamURL,
      region: rec.region, accessKey: rec.access_key_id, secretKey: secret,
    });
  } catch (e: any) {
    return errorResponse('unavailable', 'sig-v4 sign failed: ' + (e?.message ?? e), 500, true);
  }
  let upstreamResp: Response;
  try {
    upstreamResp = await fetch(upstreamURL, { method: 'GET', headers: signed });
  } catch (e: any) {
    return errorResponse('unavailable', 'upstream call failed: ' + (e?.message ?? e), 502, true);
  }
  if (upstreamResp.status >= 200 && upstreamResp.status < 300) {
    void touchCrateBucketLastUsed(env, rec.bucket_id).catch(() => {});
  }
  const outHeaders = new Headers();
  for (const h of PASSTHROUGH_RESP_HEADERS) {
    const v = upstreamResp.headers.get(h);
    if (v) outHeaders.set(h, v);
  }
  return new Response(upstreamResp.body, {
    status: upstreamResp.status,
    headers: outHeaders,
  });
}

// helper: not currently used by the worker but exported so future
// `nakli-cli crate-bucket` work can call from the same module.
export type { ErrorCode };
