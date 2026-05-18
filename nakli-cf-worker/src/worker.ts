// nakli-cf-worker — Cloudflare Worker transport for the Private Mesh.
// Spec: docs/specs/cf-worker-spec-001-v1.1.md.
//
// Single fetch() entry point dispatches by method+path. We use raw routing
// (small switch) rather than Hono to keep the dep surface tight and the
// fast-path predictable; Hono is in the deps so adapters / tools that
// embed pieces of the Worker code can use it.

import {
  Env,
  PROTOCOL_VERSION,
  NOOP_ADAPTER_NAME,
  CONFORMANCE_RETIRED_AGENT,
} from './env.js';
import {
  Freshness,
  freshnessNow,
  successResponse,
  errorResponse,
  corsResponse,
  ErrorCode,
} from './envelope.js';
import {
  parseMacaroon,
  verifySignature,
  mintMacaroon,
  base64ToBytes,
  bytesToBase64,
  ParsedGrant,
} from './macaroon.js';
import {
  evaluateCaveats,
  CaveatError,
  CaveatContext,
} from './caveats.js';
import {
  appendEvent,
  readStream,
  listStreams,
  getStream,
  HistoryConflictError,
  HistoryNamespace,
  verifyHistory,
  retirePrincipal,
  upsertPrincipal,
  getPrincipal,
  isGrantRevoked,
  markGrantRevoked,
  insertPending,
  getPending,
  idempotencyLookup,
  idempotencyPut,
  putPairing,
  getPairingByToken,
  markPairingCompleted,
  putCratePairingToken,
  getCratePairingToken,
  updateCratePairingToken,
  CratePairingTokenRecord,
} from './storage.js';
import { sha256Hex } from './hash.js';
import { ulid } from 'ulidx';

// --- request-scoped state -----------------------------------------------

interface ReqContext {
  grant: ParsedGrant;
  grantBytes: Uint8Array;
  idempotencyKey: string;
  dischargeIds: Set<string>;
  // Requester-asserted identity headers — passed through to the caveat
  // evaluator so `agent-id == X` / `device-id == X` / `principal-type in […]`
  // can cross-check.
  requesterAgentId?: string;
  requesterDeviceId?: string;
  requesterPrincipalType?: string;
  // discharge cache used during this fetch invocation only; spec talks about
  // a transport-wide cache, but for the Worker we keep it per-request +
  // backed by KV for cross-request staleness.
  dischargeCache: Set<string>;
}

// rateBuckets is *per Worker isolate*. Multiple isolates → per-isolate
// limit, which is sufficient at personal scale; we document this.
const rateBuckets = new Map<string, { capacity: number; windowMs: number; tokens: number; lastRefill: number }>();

function rateConsume(grantId: string, capacity: number, windowMs: number): boolean {
  const now = Date.now();
  let b = rateBuckets.get(grantId);
  if (!b || b.capacity !== capacity || b.windowMs !== windowMs) {
    b = { capacity, windowMs, tokens: capacity, lastRefill: now };
    rateBuckets.set(grantId, b);
  }
  const elapsed = now - b.lastRefill;
  if (elapsed > 0) {
    const rate = b.capacity / (b.windowMs / 1000);
    b.tokens = Math.min(b.capacity, b.tokens + rate * (elapsed / 1000));
    b.lastRefill = now;
  }
  if (b.tokens < 1) return false;
  b.tokens -= 1;
  return true;
}

// --- main fetch ---------------------------------------------------------

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    if (req.method === 'OPTIONS') return corsResponse();

    // One-shot conformance seeding. The first request triggers it; subsequent
    // calls are no-ops thanks to the KV write being idempotent.
    if (env.CONFORMANCE_MODE === 'true') {
      ctx.waitUntil(seedConformance(env));
    }

    const url = new URL(req.url);
    const path = url.pathname;
    const method = req.method;

    try {
      return await route(method, path, url, req, env);
    } catch (e: any) {
      console.error('worker error', { path, method, error: e?.stack ?? String(e) });
      return errorResponse('unavailable', 'internal error: ' + (e?.message ?? e), 500, true);
    }
  },
};

async function route(method: string, path: string, url: URL, req: Request, env: Env): Promise<Response> {
  // --- public endpoints ---
  if (method === 'GET' && path === '/fabric/v1/health') return handleHealth(env);
  if (method === 'GET' && path === '/fabric/v1/discover') return handleDiscover(env);
  if (method === 'POST' && path === '/fabric/v1/identity/pair/complete') return handlePairComplete(req, env);

  // CRATE-PAIR redeem is unauthenticated — the `secret` IS the auth
  // (see crate-pairing-protocol-v1.0.md §"Phase 3").
  if (method === 'POST' && path === '/v1/pairing/redeem') return handleCratePairingRedeem(req, env);

  // Conformance setup endpoint — gated by env var so it's safe to expose.
  if (method === 'POST' && path === '/fabric/v1/_conformance/setup') {
    if (env.CONFORMANCE_MODE !== 'true') {
      return errorResponse('not_found', 'endpoint not enabled', 404);
    }
    await seedConformance(env);
    return successResponse({ ok: true });
  }

  // Cluster reservation (forward-compat hook 4).
  if (path.startsWith('/fabric/v1/cluster/')) {
    return errorResponse('not_implemented', 'cluster endpoints are reserved for v2.0', 501);
  }

  // --- authenticated endpoints ---
  const auth = await authenticate(req, env);
  if (!auth.ok) return auth.response;
  const grant = auth.grant!;
  const grantBytes = auth.grantBytes!;
  const idempotencyKey = req.headers.get('X-Fabric-Idempotency-Key') ?? '';
  const dischargeIds = auth.dischargeIds!;

  const reqCtx: ReqContext = {
    grant, grantBytes, idempotencyKey, dischargeIds,
    requesterAgentId: req.headers.get('X-Fabric-Agent-Id') ?? undefined,
    requesterDeviceId: req.headers.get('X-Fabric-Device-Id') ?? undefined,
    requesterPrincipalType: req.headers.get('X-Fabric-Principal-Type') ?? undefined,
    dischargeCache: new Set(),
  };

  // --- identity ---
  if (method === 'GET' && path === '/fabric/v1/identity/principal')
    return await wrap(reqCtx, env, { primitive: 'identity', op: 'read' }, () => handleIdentityPrincipal(reqCtx, env));
  if (method === 'POST' && path === '/fabric/v1/identity/pair/initiate')
    return await wrap(reqCtx, env, { primitive: 'identity', op: 'pair' }, () => handlePairInitiate(req, env));

  // --- CRATE-PAIR (authenticated half) ---
  if (method === 'POST' && path === '/v1/pairing/intent')
    return await wrap(reqCtx, env, { primitive: 'identity', op: 'pair' }, () => handleCratePairingIntent(req, env));
  if (method === 'POST' && path === '/v1/pairing/intent/cancel')
    return await wrap(reqCtx, env, { primitive: 'identity', op: 'pair' }, () => handleCratePairingCancel(req, env));
  if (method === 'POST' && path === '/v1/capability/refresh')
    return await wrap(reqCtx, env, { primitive: 'sync', op: 'read' }, () => handleCapabilityRefresh(reqCtx, env));
  if (method === 'DELETE' && path.startsWith('/v1/capability/')) {
    const id = decodeURIComponent(path.slice('/v1/capability/'.length));
    return await wrap(reqCtx, env, { primitive: 'grant', op: 'revoke' }, () => handleCapabilityRevoke(id, env));
  }

  // --- vault ---
  if (method === 'POST' && path === '/fabric/v1/vault/append')
    return await idempotencyWrap(req, reqCtx, env, async (body) => {
      let parsed: any;
      try { parsed = JSON.parse(new TextDecoder().decode(body)); }
      catch { return errorResponse('bad_request', 'request body is not valid JSON', 400); }
      return await wrap(reqCtx, env, {
        primitive: 'vault', op: 'write', namespace: parsed?.namespace,
      }, () => handleVaultAppend(parsed, reqCtx, env));
    });
  if (method === 'GET' && path.startsWith('/fabric/v1/vault/stream/')) {
    const parts = path.split('/');
    if (parts.length !== 7) return errorResponse('bad_request', 'bad vault stream path', 400);
    const ns = decodeURIComponent(parts[5]);
    const sid = decodeURIComponent(parts[6]);
    return await wrap(reqCtx, env, { primitive: 'vault', op: 'read', namespace: ns }, () =>
      handleVaultRead(ns, sid, url, env));
  }
  if (method === 'GET' && path.startsWith('/fabric/v1/vault/streams/')) {
    const ns = decodeURIComponent(path.slice('/fabric/v1/vault/streams/'.length));
    return await wrap(reqCtx, env, { primitive: 'vault', op: 'read', namespace: ns }, () =>
      handleVaultListStreams(ns, env));
  }
  if (method === 'POST' && path === '/fabric/v1/vault/subscribe')
    return await wrap(reqCtx, env, { primitive: 'vault', op: 'read' }, async () =>
      errorResponse('not_implemented', 'vault.subscribe via SSE ships in M6.x', 501));

  // --- history ---
  if (method === 'POST' && path === '/fabric/v1/history/append')
    return await idempotencyWrap(req, reqCtx, env, async (body) => {
      let parsed: any;
      try { parsed = JSON.parse(new TextDecoder().decode(body)); }
      catch { return errorResponse('bad_request', 'request body is not valid JSON', 400); }
      return await wrap(reqCtx, env, { primitive: 'history', op: 'write' }, () =>
        handleHistoryAppend(parsed, reqCtx, env));
    });
  if (method === 'GET' && path.startsWith('/fabric/v1/history/stream/')) {
    const sid = decodeURIComponent(path.slice('/fabric/v1/history/stream/'.length));
    return await wrap(reqCtx, env, { primitive: 'history', op: 'read' }, () =>
      handleHistoryRead(sid, url, env));
  }
  if (method === 'GET' && path.startsWith('/fabric/v1/history/verify/')) {
    const sid = decodeURIComponent(path.slice('/fabric/v1/history/verify/'.length));
    return await wrap(reqCtx, env, { primitive: 'history', op: 'read' }, () => handleHistoryVerify(sid, env));
  }

  // --- grant ---
  if (method === 'POST' && path === '/fabric/v1/grant/mint')
    return await idempotencyWrap(req, reqCtx, env, async (body) => {
      let parsed: any;
      try { parsed = JSON.parse(new TextDecoder().decode(body)); }
      catch { return errorResponse('bad_request', 'request body is not valid JSON', 400); }
      return await wrap(reqCtx, env, { primitive: 'grant', op: 'mint', isDelegation: !!parsed?.parent_grant_macaroon }, () =>
        handleGrantMint(parsed, reqCtx, env));
    });
  if (method === 'POST' && path === '/fabric/v1/grant/verify')
    return await wrap(reqCtx, env, { primitive: 'grant', op: 'verify' }, async () => {
      const parsed = await req.json() as any;
      return handleGrantVerify(parsed, env);
    });
  if (method === 'POST' && path === '/fabric/v1/grant/revoke')
    return await idempotencyWrap(req, reqCtx, env, async (body) => {
      let parsed: any;
      try { parsed = JSON.parse(new TextDecoder().decode(body)); }
      catch { return errorResponse('bad_request', 'request body is not valid JSON', 400); }
      return await wrap(reqCtx, env, { primitive: 'grant', op: 'revoke' }, () =>
        handleGrantRevoke(parsed, reqCtx, env));
    });
  if (method === 'POST' && path === '/fabric/v1/grant/discharge')
    return await wrap(reqCtx, env, { primitive: 'grant', op: 'discharge' }, async () => {
      const parsed = await req.json() as any;
      return handleGrantDischarge(parsed, env);
    });

  // --- bridge ---
  if (method === 'GET' && path === '/fabric/v1/bridge/adapters')
    return await wrap(reqCtx, env, { primitive: 'bridge', op: 'read' }, async () => handleBridgeAdapters());
  if (method === 'POST' && path === '/fabric/v1/bridge/call')
    return await idempotencyWrap(req, reqCtx, env, async (body) => {
      let parsed: any;
      try { parsed = JSON.parse(new TextDecoder().decode(body)); }
      catch { return errorResponse('bad_request', 'request body is not valid JSON', 400); }
      return await handleBridgeCall(parsed, reqCtx, env);
    });
  if (method === 'POST' && path === '/fabric/v1/bridge/approve')
    return await wrap(reqCtx, env, { primitive: 'bridge', op: 'approve' }, async () => {
      const parsed = await req.json() as any;
      return handleBridgeApprove(parsed, env, reqCtx);
    });
  if (method === 'GET' && path.startsWith('/fabric/v1/bridge/pending/')) {
    const id = decodeURIComponent(path.slice('/fabric/v1/bridge/pending/'.length));
    return await wrap(reqCtx, env, { primitive: 'bridge', op: 'read' }, () => handleBridgePending(id, env));
  }

  // --- llm + sync stubs ---
  if (method === 'GET' && path === '/fabric/v1/llm/routes')
    return await wrap(reqCtx, env, { primitive: 'llm', op: 'read' }, async () =>
      successResponse({ routes: [] }, freshnessNow()));
  if (method === 'POST' && path === '/fabric/v1/llm/complete')
    return await wrap(reqCtx, env, { primitive: 'llm', op: 'invoke' }, async () =>
      errorResponse('not_implemented', 'Worker does not proxy LLM completions in v1.0', 501));
  if (method === 'GET' && path === '/fabric/v1/sync/peers')
    return await wrap(reqCtx, env, { primitive: 'sync', op: 'read' }, async () =>
      successResponse({ peers: [] }, freshnessNow()));
  if (method === 'GET' && path === '/fabric/v1/sync/pull')
    return await wrap(reqCtx, env, { primitive: 'sync', op: 'pull' }, async () =>
      errorResponse('not_implemented', 'sync.pull is single-anchor-only in v1.0', 501));
  if (method === 'POST' && path === '/fabric/v1/sync/push')
    return await wrap(reqCtx, env, { primitive: 'sync', op: 'push' }, async () =>
      errorResponse('not_implemented', 'sync.push is single-anchor-only in v1.0', 501));
  if (method === 'POST' && path === '/fabric/v1/sync/conflict-ack')
    return await wrap(reqCtx, env, { primitive: 'sync', op: 'write' }, async () =>
      errorResponse('not_implemented', 'sync.conflict-ack ships at M7', 501));

  return errorResponse('not_found', 'endpoint not found', 404);
}

// --- auth ---------------------------------------------------------------

interface AuthResult {
  ok: boolean;
  response: Response;
  grant?: ParsedGrant;
  grantBytes?: Uint8Array;
  dischargeIds?: Set<string>;
}

async function authenticate(req: Request, env: Env): Promise<AuthResult> {
  const raw = req.headers.get('X-Fabric-Grant');
  if (!raw) return { ok: false, response: errorResponse('grant_missing', 'X-Fabric-Grant header missing', 401) };
  let bytes: Uint8Array;
  try { bytes = base64ToBytes(raw.trim()); }
  catch { return { ok: false, response: errorResponse('grant_invalid', 'X-Fabric-Grant header is not valid base64', 401) }; }
  const rootKey = base64ToBytes(env.MACAROON_ROOT_KEY);
  try {
    verifySignature(bytes, rootKey);
  } catch (e: any) {
    return { ok: false, response: errorResponse('grant_invalid', 'macaroon signature verification failed', 401) };
  }
  const grant = parseMacaroon(bytes);
  if (await isGrantRevoked(env, grant.grantId)) {
    return { ok: false, response: errorResponse('grant_revoked', 'Grant has been revoked', 401) };
  }
  const agentId = req.headers.get('X-Fabric-Agent-Id');
  if (agentId) {
    const p = await getPrincipal(env, agentId);
    if (p?.retired_at) {
      return { ok: false, response: errorResponse('principal_retired', 'agent principal is retired', 401) };
    }
  }
  // Parse discharges (X-Fabric-Discharge: comma-separated base64 macaroons).
  const dischargeIds = new Set<string>();
  const dh = req.headers.get('X-Fabric-Discharge');
  if (dh) {
    for (const part of dh.split(',').map((s) => s.trim()).filter(Boolean)) {
      try {
        const macBytes = base64ToBytes(part);
        verifySignature(macBytes, rootKey, (c) => {
          if (c.startsWith('time < ')) {
            const t = new Date(c.slice('time < '.length).trim());
            if (Number.isNaN(t.getTime()) || new Date() >= t) return new Error('discharge expired');
          }
          return null;
        });
        const parsed = parseMacaroon(macBytes);
        dischargeIds.add(parsed.identifier.grant_id);
      } catch (e: any) {
        return { ok: false, response: errorResponse('grant_invalid', 'discharge verification failed: ' + (e?.message ?? e), 401) };
      }
    }
  }
  return { ok: true, response: undefined as unknown as Response, grant, grantBytes: bytes, dischargeIds };
}

// --- scope + caveat enforcement -----------------------------------------

interface ScopeReq {
  primitive: string;
  op: string;
  namespace?: string;
  isDelegation?: boolean;
  isBridgeCall?: boolean;
  bridgeDomain?: string;
  bridgeAmount?: number;
  bridgeCurrency?: string;
}

async function wrap(reqCtx: ReqContext, env: Env, sr: ScopeReq, handler: () => Promise<Response>): Promise<Response> {
  // Scope.
  const scope = reqCtx.grant.identifier.scope;
  if (scope.primitive && scope.primitive !== sr.primitive) {
    return errorResponse('scope_denied', 'Grant scope primitive does not authorize this operation', 403);
  }
  if (sr.namespace && scope.namespace && scope.namespace !== '*' && scope.namespace !== sr.namespace) {
    return errorResponse('scope_denied', 'Grant scope namespace does not authorize this stream', 403);
  }
  if (sr.op && scope.operations && scope.operations.length > 0 && !scope.operations.includes(sr.op)) {
    return errorResponse('scope_denied', "Grant scope operations do not include this request's operation", 403);
  }
  // Caveats.
  try {
    const ctx: CaveatContext = {
      now: new Date(),
      operation: sr.op,
      namespace: sr.namespace ?? '',
      primitive: sr.primitive,
      hasIdempotencyKey: reqCtx.idempotencyKey !== '',
      isDelegationRequest: !!sr.isDelegation,
      grantId: reqCtx.grant.grantId,
      requesterPrincipalType: reqCtx.requesterPrincipalType,
      requesterAgentId: reqCtx.requesterAgentId,
      requesterDeviceId: reqCtx.requesterDeviceId,
      isBridgeCall: !!sr.isBridgeCall,
      bridgeDomain: sr.bridgeDomain,
      bridgeAmount: sr.bridgeAmount,
      bridgeCurrency: sr.bridgeCurrency,
      rateConsume: (gid, cap, win) => rateConsume(gid, cap, win),
      dischargeIds: reqCtx.dischargeIds,
      dischargeLookup: () => false,
    };
    evaluateCaveats(reqCtx.grant.caveats, ctx);
  } catch (e: any) {
    if (e instanceof CaveatError) {
      if (e.kind === 'rate_limited') return errorResponse('rate_limited', e.message, 429, true);
      if (e.kind === 'human_approval') {
        // Caller is responsible for queueing the pending bridge call; bubble up.
        return await handler().catch((err) => errorResponse('unavailable', err?.message ?? String(err), 500, true));
      }
      return errorResponse('caveat_unmet', e.message, 403);
    }
    return errorResponse('unavailable', String(e?.message ?? e), 500, true);
  }
  return handler();
}

// readScopeFromCaveats — see caveats path: when human-approval triggers from
// inside wrap(), the handler still needs to know. We re-evaluate in the
// bridge handler explicitly below.
function caveatsContainHumanApproval(caveats: string[]): boolean {
  return caveats.some((c) => c.trim() === 'requires-human-approval');
}

// --- idempotency wrapper -----------------------------------------------

async function idempotencyWrap(req: Request, reqCtx: ReqContext, env: Env, run: (body: Uint8Array) => Promise<Response>): Promise<Response> {
  if (!reqCtx.idempotencyKey) {
    return errorResponse('bad_request', 'X-Fabric-Idempotency-Key is required on state-changing requests', 400);
  }
  const body = new Uint8Array(await req.arrayBuffer());
  const payloadHash = await sha256Hex(body);
  const lookup = await idempotencyLookup(env, reqCtx.idempotencyKey, reqCtx.grant.grantId, payloadHash);
  if (lookup.replay) {
    return new Response(lookup.bodyBytes, {
      status: lookup.status,
      headers: { 'Content-Type': 'application/json', 'X-Fabric-Version': PROTOCOL_VERSION },
    });
  }
  if (lookup.conflict) {
    return errorResponse('idempotency_conflict', 'idempotency key reused with different payload', 409);
  }
  const resp = await run(body);
  // Buffer + persist on success.
  if (resp.status >= 200 && resp.status < 300) {
    const buf = new Uint8Array(await resp.clone().arrayBuffer());
    const retention = parseInt(env.IDEMPOTENCY_RETENTION_SECONDS, 10) || 86400;
    await idempotencyPut(env, reqCtx.idempotencyKey, reqCtx.grant.grantId, payloadHash, resp.status, buf, retention);
  }
  return resp;
}

// --- handlers -----------------------------------------------------------

async function handleHealth(env: Env): Promise<Response> {
  const peers = (env.PEER_URL ?? '').split(',').map((s) => s.trim()).filter(Boolean);
  const peerHealth: any[] = [];
  const reasons: string[] = [];
  let degraded = false;
  for (const url of peers) {
    let reachable = false;
    try {
      const r = await fetch(url, { method: 'GET' });
      reachable = r.status < 500;
    } catch {
      reachable = false;
    }
    peerHealth.push({ peer: url, reachable });
    if (!reachable) { degraded = true; reasons.push('peer unreachable: ' + url); }
  }
  return successResponse({
    transport_id: env.HUB_ID,
    version: env.PROTOCOL_VERSION,
    binary_version: 'nakli-cf-worker/0.1.0',
    uptime_seconds: 0, // Worker has no notion of uptime
    degraded,
    degraded_reasons: reasons,
    peer_health: peerHealth,
    event_count: 0, // could be computed by walking KV; skipped for speed
    principals_count: {},
  });
}

function handleDiscover(env: Env): Response {
  return successResponse({
    transport_type: 'cf-worker',
    transport_id: env.HUB_ID,
    version: env.PROTOCOL_VERSION,
    supported_primitives: ['vault', 'history', 'sync', 'grant', 'identity', 'llm', 'bridge'],
    supported_caveats: [
      'time', 'principal-type', 'agent-id', 'device-id', 'operation', 'namespace',
      'rate', 'max-amount', 'only-domain', 'requires-human-approval',
      'nondelegatable', 'idempotency-required', 'discharge-from',
    ],
    max_event_size_bytes: parseInt(env.MAX_EVENT_SIZE_BYTES, 10),
    max_idempotency_window_seconds: parseInt(env.IDEMPOTENCY_RETENTION_SECONDS, 10),
  });
}

async function handleIdentityPrincipal(reqCtx: ReqContext, env: Env): Promise<Response> {
  return successResponse({
    principal_id: reqCtx.grant.identifier.issued_by_principal,
  });
}

async function handlePairInitiate(req: Request, env: Env): Promise<Response> {
  const body = await req.json() as any;
  const token = ulid();
  const numeric = Math.floor(100000 + Math.random() * 900000).toString();
  const expiresAt = new Date(Date.now() + 600_000).toISOString();
  const rec = {
    token,
    numeric_code: numeric,
    initiated_by_principal: '',
    initiated_at: new Date().toISOString(),
    expires_at: expiresAt,
  };
  await putPairing(env, rec, 600);
  return successResponse({
    pairing_token: token,
    numeric_code: numeric,
    qr_payload: token,
    magic_link: '',
    expires_at: expiresAt,
  });
}

async function handlePairComplete(req: Request, env: Env): Promise<Response> {
  const body = await req.json() as any;
  if (!body?.pairing_token) return errorResponse('bad_request', 'pairing_token required', 400);
  const rec = await getPairingByToken(env, body.pairing_token);
  if (!rec) return errorResponse('not_found', 'pairing token unknown', 404);
  if (rec.completed_at) return errorResponse('conflict', 'pairing token already completed', 409);
  rec.completed_at = new Date().toISOString();
  rec.completed_by_device = body.new_device_name ?? '';
  await markPairingCompleted(env, rec);
  return successResponse({
    device_id: ulid(),
    enrollment_grant: '',
    transport_configs: [{ type: 'cf-worker', url: '' }],
  });
}

async function handleVaultAppend(body: any, reqCtx: ReqContext, env: Env): Promise<Response> {
  if (!body?.namespace || !body?.stream_id || !body?.event?.kind) {
    return errorResponse('bad_request', 'namespace, stream_id, and event.kind are required', 400);
  }
  if (body.namespace.startsWith('fabric.')) {
    return errorResponse('scope_denied', 'fabric.* namespaces are reserved', 403);
  }
  const ciphertext = base64ToBytes(body.event.payload_ciphertext ?? '');
  if (ciphertext.length > parseInt(env.MAX_EVENT_SIZE_BYTES, 10)) {
    return errorResponse('bad_request', 'event payload exceeds max_event_size_bytes', 400);
  }
  const res = await appendEvent(env, {
    namespace: body.namespace,
    stream_id: body.stream_id,
    stream_type: 'vault',
    kind: body.event.kind,
    payload_ciphertext: ciphertext,
    payload_metadata: body.event.payload_metadata,
    causal_dependencies: body.event.causal_dependencies,
    vector_clock: body.event.vector_clock,
    appended_by_principal: reqCtx.grant.identifier.issued_by_principal,
    appended_by_grant_id: reqCtx.grant.grantId,
  });
  return successResponse({ event_id: res.event_id, sequence_number: res.sequence_number });
}

async function handleVaultRead(ns: string, sid: string, url: URL, env: Env): Promise<Response> {
  if (!ns || !sid) return errorResponse('bad_request', 'namespace and stream_id are required', 400);
  const limit = parseInt(url.searchParams.get('limit') ?? '0', 10) || 100;
  const since = url.searchParams.get('since') ?? undefined;
  const { events, more } = await readStream(env, ns, sid, { limit, sinceEventId: since });
  return successResponse({ events: events.map(mapEventForWire), more });
}

async function handleVaultListStreams(ns: string, env: Env): Promise<Response> {
  const streams = await listStreams(env, ns);
  return successResponse({
    streams: streams.map((s) => ({
      stream_id: s.stream_id,
      latest_event_id: s.head_event_id,
      event_count: s.event_count,
    })),
  });
}

async function handleHistoryAppend(body: any, reqCtx: ReqContext, env: Env): Promise<Response> {
  if (!body?.stream_id || !body?.event?.kind) {
    return errorResponse('bad_request', 'stream_id and event.kind are required', 400);
  }
  const ciphertext = base64ToBytes(body.event.payload_ciphertext ?? '');
  const prev = body.event.previous_event_hash ? base64ToBytes(body.event.previous_event_hash) : null;
  try {
    const res = await appendEvent(env, {
      namespace: HistoryNamespace,
      stream_id: body.stream_id,
      stream_type: 'history',
      kind: body.event.kind,
      payload_ciphertext: ciphertext,
      payload_metadata: body.event.payload_metadata,
      causal_dependencies: body.event.causal_dependencies,
      vector_clock: body.event.vector_clock,
      previous_event_hash: prev,
      appended_by_principal: reqCtx.grant.identifier.issued_by_principal,
      appended_by_grant_id: reqCtx.grant.grantId,
    });
    return successResponse({
      event_id: res.event_id,
      event_hash: res.event_hash,
      sequence_number: res.sequence_number,
    });
  } catch (e: any) {
    if (e instanceof HistoryConflictError) {
      return errorResponse('conflict', e.message, 409);
    }
    throw e;
  }
}

async function handleHistoryRead(sid: string, url: URL, env: Env): Promise<Response> {
  const limit = parseInt(url.searchParams.get('limit') ?? '0', 10) || 100;
  const since = url.searchParams.get('since') ?? undefined;
  const { events, more } = await readStream(env, HistoryNamespace, sid, { limit, sinceEventId: since });
  return successResponse({ events: events.map(mapEventForWire), more });
}

async function handleHistoryVerify(sid: string, env: Env): Promise<Response> {
  const res = await verifyHistory(env, sid);
  return successResponse(res);
}

function mapEventForWire(ev: any): any {
  return {
    event_id: ev.event_id,
    kind: ev.kind,
    sequence_number: ev.sequence_number,
    payload_ciphertext: ev.payload_ciphertext ?? '',
    payload_metadata: ev.payload_metadata,
    causal_dependencies: ev.causal_dependencies ?? [],
    vector_clock: ev.vector_clock ?? {},
    previous_event_hash: ev.previous_event_hash ?? '',
    event_hash: ev.event_hash ?? '',
    appended_at: ev.appended_at,
    appended_by_principal: ev.appended_by_principal,
  };
}

// --- grant handlers -----------------------------------------------------

async function handleGrantMint(body: any, reqCtx: ReqContext, env: Env): Promise<Response> {
  if (!body?.recipient_principal_id || !body?.scope?.primitive) {
    return errorResponse('bad_request', 'recipient_principal_id and scope.primitive are required', 400);
  }
  // Narrowing — when parent_grant_macaroon supplied, enforce scope subset +
  // caveat superset (mirrors the Hub's M3 logic).
  if (body.parent_grant_macaroon) {
    let parent;
    try {
      parent = parseMacaroon(base64ToBytes(body.parent_grant_macaroon));
      verifySignature(parent.macaroon, base64ToBytes(env.MACAROON_ROOT_KEY));
    } catch (e: any) {
      return errorResponse('grant_invalid', 'parent_grant_macaroon: ' + (e?.message ?? e), 403);
    }
    if (parent.identifier.scope.primitive && parent.identifier.scope.primitive !== body.scope.primitive) {
      return errorResponse('scope_denied', "child grant scope.primitive must equal parent's", 403);
    }
    const pns = parent.identifier.scope.namespace;
    if (pns && pns !== '*' && pns !== body.scope.namespace) {
      return errorResponse('scope_denied', "child grant scope.namespace must equal parent's (or parent must be wildcard)", 403);
    }
    if (parent.identifier.scope.operations?.length) {
      for (const op of (body.scope.operations ?? [])) {
        if (!parent.identifier.scope.operations.includes(op)) {
          return errorResponse('scope_denied', "child grant scope.operations must be a subset of parent's", 403);
        }
      }
    }
    const childCaveats: string[] = body.caveats ?? [];
    for (const pc of parent.caveats) {
      const t = pc.trim();
      if (t.startsWith('time < ')) continue;
      if (!childCaveats.map((c) => c.trim()).includes(t)) {
        return errorResponse('scope_denied', 'child grant must carry every caveat from parent; missing: ' + t, 403);
      }
    }
  }
  const now = new Date();
  const expiresAt = body.expires_at ? new Date(body.expires_at) : new Date(now.getTime() + 30 * 24 * 3600 * 1000);
  const grantId = ulid();
  const id = {
    grant_id: grantId,
    issued_at: now.toISOString(),
    issued_by_principal: reqCtx.grant.identifier.issued_by_principal,
    issued_by_keypair: reqCtx.grant.identifier.issued_by_keypair ?? new Uint8Array(0),
    parent_grant_id: reqCtx.grant.grantId,
    scope: {
      primitive: body.scope.primitive,
      namespace: body.scope.namespace ?? '*',
      operations: body.scope.operations ?? [],
    },
  };
  const caveats = ['time < ' + expiresAt.toISOString(), ...(body.caveats ?? [])];
  const { macaroon } = mintMacaroon({
    rootKey: base64ToBytes(env.MACAROON_ROOT_KEY),
    location: 'cf-worker',
    identifier: id,
    caveats,
  });
  return successResponse({ grant_id: grantId, macaroon: bytesToBase64(macaroon) });
}

async function handleGrantVerify(body: any, env: Env): Promise<Response> {
  if (!body?.macaroon) return errorResponse('bad_request', 'macaroon is required', 400);
  let macBytes: Uint8Array;
  try { macBytes = base64ToBytes(body.macaroon); }
  catch { return errorResponse('bad_request', 'macaroon is not valid base64', 400); }
  const reasons: string[] = [];
  let wouldSucceed = true;
  try { verifySignature(macBytes, base64ToBytes(env.MACAROON_ROOT_KEY)); }
  catch (e: any) {
    return successResponse({ would_succeed: false, reasons: ['signature_invalid: ' + e?.message] });
  }
  let parsed;
  try { parsed = parseMacaroon(macBytes); }
  catch (e: any) { return successResponse({ would_succeed: false, reasons: ['parse_failed: ' + e?.message] }); }
  const hop = body.hypothetical_operation ?? {};
  if (parsed.identifier.scope.primitive && parsed.identifier.scope.primitive !== hop.primitive) {
    wouldSucceed = false;
    reasons.push('scope.primitive does not authorize the hypothetical operation');
  }
  if (parsed.identifier.scope.namespace && parsed.identifier.scope.namespace !== '*' && parsed.identifier.scope.namespace !== hop.namespace) {
    wouldSucceed = false;
    reasons.push('scope.namespace does not authorize the hypothetical operation');
  }
  if (parsed.identifier.scope.operations?.length && !parsed.identifier.scope.operations.includes(hop.operation)) {
    wouldSucceed = false;
    reasons.push('scope.operations does not include the hypothetical operation');
  }
  return successResponse({ would_succeed: wouldSucceed, reasons });
}

async function handleGrantRevoke(body: any, reqCtx: ReqContext, env: Env): Promise<Response> {
  if (!body?.grant_id) return errorResponse('bad_request', 'grant_id is required', 400);
  // Append a revocation event to the history stream and mark the grant.
  const event = { reason: body.reason ?? '', revoked_by: reqCtx.grant.identifier.issued_by_principal, revoked_at: new Date().toISOString(), grant_id: body.grant_id };
  const ciphertext = new TextEncoder().encode(JSON.stringify(event));
  // Read current head for the chain.
  const stream = await getStream(env, HistoryNamespace, 'revocations');
  const prev = stream?.head_event_hash ? base64ToBytes(stream.head_event_hash) : null;
  const res = await appendEvent(env, {
    namespace: HistoryNamespace,
    stream_id: 'revocations',
    stream_type: 'history',
    kind: 'grant.revoked',
    payload_ciphertext: ciphertext,
    appended_by_principal: reqCtx.grant.identifier.issued_by_principal,
    appended_by_grant_id: reqCtx.grant.grantId,
    previous_event_hash: prev,
  });
  await markGrantRevoked(env, body.grant_id, res.event_id);
  return successResponse({ revocation_event_id: res.event_id });
}

async function handleGrantDischarge(body: any, env: Env): Promise<Response> {
  if (!body?.grant_id || !body?.verifier_url) {
    return errorResponse('bad_request', 'grant_id and verifier_url are required', 400);
  }
  if (await isGrantRevoked(env, body.grant_id)) {
    return errorResponse('grant_revoked', 'Grant has been revoked; no fresh discharge will be issued', 403);
  }
  const expires = new Date(Date.now() + (parseInt(env.DISCHARGE_TTL_SECONDS, 10) || 86400) * 1000);
  const id = {
    grant_id: body.verifier_url,
    issued_at: new Date().toISOString(),
    issued_by_principal: '',
    issued_by_keypair: new Uint8Array(0),
    scope: { primitive: '', namespace: '', operations: [] },
  };
  const { macaroon } = mintMacaroon({
    rootKey: base64ToBytes(env.MACAROON_ROOT_KEY),
    location: body.verifier_url,
    identifier: id,
    caveats: ['time < ' + expires.toISOString()],
  });
  return successResponse({ discharge: bytesToBase64(macaroon), expires_at: expires.toISOString() });
}

// --- bridge handlers ----------------------------------------------------

function handleBridgeAdapters(): Response {
  // Worker hosts only the conformance-test noop adapter in v1.0 (mirrors
  // cf-worker-spec §Bridge: real adapter execution belongs on the Hub).
  return successResponse({
    adapters: [
      {
        name: NOOP_ADAPTER_NAME,
        version: '1.0.0',
        operations: [
          { name: 'echo', description: 'Echo params as result.echo.', params: [], side_effects: false, estimable: false },
          { name: 'transfer', description: 'Inert transfer used by conformance tests for max-amount / only-domain caveats.', params: [], side_effects: false, estimable: false },
          { name: 'fetch', description: 'Inert fetch — conformance only.', params: [], side_effects: false, estimable: false },
        ],
        status: 'active',
      },
    ],
  });
}

async function handleBridgeCall(body: any, reqCtx: ReqContext, env: Env): Promise<Response> {
  if (!reqCtx.idempotencyKey) {
    return errorResponse('bad_request', 'X-Fabric-Idempotency-Key is required on Bridge calls', 400);
  }
  // Use wrap() with bridge scope + caveats; if requires-human-approval is
  // present, the wrap() returns via human-approval branch so we queue.
  const scope = { primitive: 'bridge', op: 'call', isBridgeCall: true, bridgeDomain: body?.domain, bridgeAmount: body?.amount, bridgeCurrency: body?.currency };
  // Inline scope check + caveat eval (so we can produce the 202 below cleanly).
  const sc = reqCtx.grant.identifier.scope;
  if (sc.primitive && sc.primitive !== 'bridge') return errorResponse('scope_denied', 'Grant scope primitive does not authorize this operation', 403);
  if (sc.operations?.length && !sc.operations.includes('call')) return errorResponse('scope_denied', "Grant scope operations do not include this request's operation", 403);
  try {
    evaluateCaveats(reqCtx.grant.caveats, {
      now: new Date(),
      operation: 'call',
      namespace: '',
      primitive: 'bridge',
      hasIdempotencyKey: true,
      isDelegationRequest: false,
      grantId: reqCtx.grant.grantId,
      isBridgeCall: true,
      bridgeDomain: body?.domain,
      bridgeAmount: body?.amount,
      bridgeCurrency: body?.currency,
      rateConsume,
      dischargeIds: reqCtx.dischargeIds,
      dischargeLookup: () => false,
    });
  } catch (e: any) {
    if (e instanceof CaveatError) {
      if (e.kind === 'rate_limited') return errorResponse('rate_limited', e.message, 429, true);
      if (e.kind === 'human_approval') {
        const pendingId = ulid();
        await insertPending(env, {
          pending_id: pendingId,
          grant_id: reqCtx.grant.grantId,
          adapter: body?.adapter ?? '',
          operation: body?.operation ?? '',
          params: body?.params ?? {},
          requested_by_principal: reqCtx.grant.identifier.issued_by_principal,
          requested_at: new Date().toISOString(),
        });
        return new Response(JSON.stringify({
          ok: false,
          error: { code: 'human_approval_required', message: 'this bridge call requires human approval; see pending_id', retryable: false },
          data: { pending_id: pendingId, status: 'pending' },
        }), { status: 202, headers: { 'Content-Type': 'application/json', 'X-Fabric-Version': PROTOCOL_VERSION } });
      }
      return errorResponse('caveat_unmet', e.message, 403);
    }
    throw e;
  }
  // Dispatch — only conformance-test noop adapter is supported on the Worker.
  if (body?.adapter !== NOOP_ADAPTER_NAME) {
    return errorResponse('not_found', `bridge adapter "${body?.adapter}" not registered on this Worker`, 404);
  }
  return successResponse({
    adapter: body.adapter,
    operation: body.operation,
    result: { echo: body?.params ?? {}, adapter: body.adapter, operation: body.operation },
    metrics: { duration_ms: 1, bytes_in: 0, bytes_out: 0 },
  });
}

async function handleBridgeApprove(body: any, env: Env, reqCtx: ReqContext): Promise<Response> {
  if (!body?.pending_id) return errorResponse('bad_request', 'pending_id is required', 400);
  const p = await getPending(env, body.pending_id);
  if (!p) return errorResponse('not_found', 'pending bridge id not found', 404);
  p.approved_at = new Date().toISOString();
  await insertPending(env, p);
  return successResponse({ approved: true });
}

async function handleBridgePending(id: string, env: Env): Promise<Response> {
  const p = await getPending(env, id);
  if (!p) return errorResponse('not_found', 'pending bridge id not found', 404);
  const status = p.approved_at ? 'approved' : 'pending';
  return successResponse({ pending_id: p.pending_id, status, adapter: p.adapter, operation: p.operation });
}

// --- CRATE-PAIR handlers (Unit C parity) --------------------------------
//
// Mirrors nakli-hub/internal/server/handlers_crate_pairing.go. Schema +
// auth model + error code mapping match the Hub-side implementation;
// storage uses KV instead of SQLite. See plan/Unit-C-notes.md for the
// known KV-vs-SQLite atomicity difference (eventually-consistent
// read-then-conditional-write; race window documented for v1.0).

const EXPECTED_TOKEN_TYPE = 'crate.pairing.token';
const CRATE_PAIR_CURRENT_VERSION = 1;
const CRATE_PAIR_CAPABILITY_TTL_SECONDS = 365 * 24 * 3600;

interface CratePairingIntentPayload {
  v: number;
  type: string;
  secret: string;
  transport_endpoint: string;
  transport_type: string;
  bucket_id: string;
  identity_pubkey: string;
  issued_at: number;
  expires_at: number;
}

function validateCratePairingPayload(p: CratePairingIntentPayload, nowSec: number): { code: ErrorCode | null; status: number; message: string } {
  if (!p.v) return { code: 'bad_request', status: 400, message: 'v is required' };
  if (p.v !== CRATE_PAIR_CURRENT_VERSION) return { code: 'protocol_version', status: 426, message: 'unsupported protocol version' };
  if (p.type !== EXPECTED_TOKEN_TYPE) return { code: 'bad_request', status: 400, message: 'type must be "' + EXPECTED_TOKEN_TYPE + '"' };
  if (!p.secret) return { code: 'bad_request', status: 400, message: 'secret is required' };
  if (!p.transport_endpoint || !p.transport_type) return { code: 'bad_request', status: 400, message: 'transport_endpoint and transport_type are required' };
  if (!p.bucket_id) return { code: 'bad_request', status: 400, message: 'bucket_id is required' };
  if (!p.identity_pubkey) return { code: 'bad_request', status: 400, message: 'identity_pubkey is required' };
  if (!p.issued_at || !p.expires_at) return { code: 'bad_request', status: 400, message: 'issued_at and expires_at are required' };
  if (p.expires_at <= p.issued_at) return { code: 'bad_request', status: 400, message: 'expires_at must be > issued_at' };
  if (p.expires_at < nowSec) return { code: 'token_expired', status: 410, message: 'token expires_at is in the past' };
  return { code: null, status: 0, message: '' };
}

async function handleCratePairingIntent(req: Request, env: Env): Promise<Response> {
  const raw = await req.text();
  if (raw.length > 64 * 1024) {
    return errorResponse('bad_request', 'request body exceeds 64 KB', 400);
  }
  let payload: CratePairingIntentPayload;
  try {
    payload = JSON.parse(raw);
  } catch {
    return errorResponse('bad_request', 'request body is not valid JSON', 400);
  }

  const nowSec = Math.floor(Date.now() / 1000);
  const v = validateCratePairingPayload(payload, nowSec);
  if (v.code) return errorResponse(v.code, v.message, v.status);

  // Conflict on existing secret is treated as idempotent success.
  const existing = await getCratePairingToken(env, payload.secret);
  if (existing) {
    return successResponse({}, freshnessNow(), 201);
  }

  const ttlSec = Math.max(60, payload.expires_at - nowSec);
  const rec: CratePairingTokenRecord = {
    secret: payload.secret,
    payload_json: raw,
    bucket_id: payload.bucket_id,
    identity_pubkey: payload.identity_pubkey,
    transport_endpoint: payload.transport_endpoint,
    transport_type: payload.transport_type,
    issued_at: new Date(payload.issued_at * 1000).toISOString(),
    expires_at: new Date(payload.expires_at * 1000).toISOString(),
    created_at: new Date().toISOString(),
  };
  await putCratePairingToken(env, rec, ttlSec);
  return successResponse({}, freshnessNow(), 201);
}

async function handleCratePairingCancel(req: Request, env: Env): Promise<Response> {
  const body = await req.json() as { secret?: string };
  if (!body?.secret) return errorResponse('bad_request', 'secret is required', 400);
  const existing = await getCratePairingToken(env, body.secret);
  if (!existing) return errorResponse('token_not_found', 'no pairing intent matches that secret', 404);
  if (existing.redeemed_at) {
    return errorResponse('token_already_redeemed', 'token already redeemed; revoke the issued capability via DELETE /v1/capability/{id}', 409);
  }
  if (existing.cancelled_at) {
    // Idempotent.
    return new Response(null, { status: 204 });
  }
  existing.cancelled_at = new Date().toISOString();
  await updateCratePairingToken(env, existing);
  return new Response(null, { status: 204 });
}

async function handleCratePairingRedeem(req: Request, env: Env): Promise<Response> {
  let body: { v?: number; secret?: string; daemon_pubkey?: string; daemon_fingerprint?: unknown };
  try {
    body = await req.json();
  } catch {
    return errorResponse('bad_request', 'request body is not valid JSON', 400);
  }
  if (body.v !== CRATE_PAIR_CURRENT_VERSION) {
    return errorResponse('protocol_version', 'unsupported protocol version', 426);
  }
  if (!body.secret || !body.daemon_pubkey) {
    return errorResponse('bad_request', 'secret and daemon_pubkey are required', 400);
  }
  const existing = await getCratePairingToken(env, body.secret);
  if (!existing) return errorResponse('token_not_found', 'token not recognised', 404);
  if (existing.cancelled_at) return errorResponse('token_cancelled', 'token was cancelled by the issuer', 404);
  if (existing.redeemed_at) return errorResponse('token_already_redeemed', 'token already redeemed; tokens are single-use', 409);
  if (new Date(existing.expires_at).getTime() < Date.now()) {
    return errorResponse('token_expired', 'token has expired; generate a new one from the browser', 410);
  }

  // Mint the daemon capability — macaroon scoped to sync over the bucket
  // namespace with `time < now+1y`, `device-id == daemon_pubkey`,
  // `operation in [read, write]`.
  const now = new Date();
  const expires = new Date(now.getTime() + CRATE_PAIR_CAPABILITY_TTL_SECONDS * 1000);
  const grantId = ulid();
  const transportPubkey = base64ToBytes(env.HUB_PUBLIC_KEY);
  const { macaroon } = mintMacaroon({
    rootKey: base64ToBytes(env.MACAROON_ROOT_KEY),
    location: 'cf-worker',
    identifier: {
      grant_id: grantId,
      issued_at: now.toISOString(),
      issued_by_principal: env.HUB_ID,
      issued_by_keypair: transportPubkey,
      scope: {
        primitive: 'sync',
        namespace: existing.bucket_id,
        operations: ['read', 'write'],
      },
    },
    caveats: [
      'time < ' + expires.toISOString(),
      'device-id == ' + body.daemon_pubkey,
      'operation in [read, write]',
    ],
  });

  // Mark redeemed via read-then-conditional-write. KV has no atomic CAS
  // so a concurrent redeem call could win the race — re-read to check
  // and bail if so. Race window is documented per plan/Unit-C-notes.md.
  const reread = await getCratePairingToken(env, body.secret);
  if (!reread || reread.redeemed_at) {
    return errorResponse('token_already_redeemed', 'token already redeemed by a concurrent caller', 409);
  }
  reread.redeemed_at = now.toISOString();
  reread.redeemed_by_daemon_pubkey = body.daemon_pubkey;
  reread.daemon_fingerprint = JSON.stringify(body.daemon_fingerprint ?? {});
  reread.issued_capability_id = grantId;
  await updateCratePairingToken(env, reread);

  return successResponse({
    v: 1,
    capability: bytesToBase64(macaroon),
    bucket_reference: existing.bucket_id,
    transport_pubkey: bytesToBase64(transportPubkey),
    expires_at: Math.floor(expires.getTime() / 1000),
  });
}

async function handleCapabilityRefresh(reqCtx: ReqContext, env: Env): Promise<Response> {
  // Caller authenticates with their current capability — wrap()'s scope
  // check already ran (primitive=sync, op=read). Verify the capability
  // isn't revoked; mint a fresh one with the same scope + non-time caveats.
  if (await isGrantRevoked(env, reqCtx.grant.grantId)) {
    return errorResponse('grant_revoked', 'capability has been revoked; re-pair to obtain a new one', 401);
  }
  const now = new Date();
  const expires = new Date(now.getTime() + CRATE_PAIR_CAPABILITY_TTL_SECONDS * 1000);
  const newGrantId = ulid();
  const transportPubkey = base64ToBytes(env.HUB_PUBLIC_KEY);

  // Preserve scope + non-time caveats from the current capability.
  const newCaveats: string[] = ['time < ' + expires.toISOString()];
  for (const c of reqCtx.grant.caveats) {
    const trimmed = c.trim();
    if (trimmed.startsWith('time < ')) continue;
    newCaveats.push(trimmed);
  }

  const { macaroon } = mintMacaroon({
    rootKey: base64ToBytes(env.MACAROON_ROOT_KEY),
    location: 'cf-worker',
    identifier: {
      grant_id: newGrantId,
      issued_at: now.toISOString(),
      issued_by_principal: env.HUB_ID,
      issued_by_keypair: transportPubkey,
      parent_grant_id: reqCtx.grant.grantId,
      scope: reqCtx.grant.identifier.scope,
    },
    caveats: newCaveats,
  });
  return successResponse({
    v: 1,
    capability: bytesToBase64(macaroon),
    expires_at: Math.floor(expires.getTime() / 1000),
  });
}

async function handleCapabilityRevoke(grantId: string, env: Env): Promise<Response> {
  if (!grantId) return errorResponse('bad_request', 'capability id is required', 400);
  await markGrantRevoked(env, grantId, '');
  return new Response(null, { status: 204 });
}

// --- conformance setup --------------------------------------------------

let seededOnce = false;

async function seedConformance(env: Env): Promise<void> {
  if (seededOnce) return;
  await retirePrincipal(env, CONFORMANCE_RETIRED_AGENT, 'cf-worker-conformance-setup');
  seededOnce = true;
}
