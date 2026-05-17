// Caveat enforcement, mirroring nakli-hub/internal/server/caveats.go.
// The Worker shares the protocol's caveat catalogue verbatim.

export interface CaveatContext {
  now: Date;
  operation: string;
  namespace: string;
  primitive: string;
  hasIdempotencyKey: boolean;
  isDelegationRequest: boolean;
  grantId: string;
  requesterPrincipalType?: string;
  requesterAgentId?: string;
  requesterDeviceId?: string;
  isBridgeCall: boolean;
  bridgeDomain?: string;
  bridgeAmount?: number;
  bridgeCurrency?: string;
  // rateConsume implements the token bucket; returns true when the call is
  // allowed.
  rateConsume(grantId: string, capacity: number, windowMs: number): boolean;
  // dischargeIds is the set of verifier-ids the caller supplied.
  dischargeIds: Set<string>;
  // dischargeCache lookup for a previously-cached discharge.
  dischargeLookup(verifierUrl: string): boolean;
}

export type CaveatKind = 'unmet' | 'rate_limited' | 'human_approval';

export class CaveatError extends Error {
  kind: CaveatKind;
  caveat: string;
  reason: string;
  constructor(caveat: string, reason: string, kind: CaveatKind = 'unmet') {
    super(`caveat unmet: ${caveat}: ${reason}`);
    this.name = 'CaveatError';
    this.caveat = caveat;
    this.reason = reason;
    this.kind = kind;
  }
}

export function evaluateCaveats(caveats: string[], ctx: CaveatContext): void {
  for (const raw of caveats) {
    const c = raw.trim();
    evaluateOne(c, ctx);
  }
}

function evaluateOne(c: string, ctx: CaveatContext): void {
  if (c.startsWith('time < ')) {
    const t = new Date(c.slice('time < '.length).trim());
    if (Number.isNaN(t.getTime())) throw new CaveatError(c, 'bad timestamp');
    if (ctx.now >= t) throw new CaveatError(c, 'expired');
    return;
  }
  if (c.startsWith('time > ')) {
    const t = new Date(c.slice('time > '.length).trim());
    if (Number.isNaN(t.getTime())) throw new CaveatError(c, 'bad timestamp');
    if (ctx.now <= t) throw new CaveatError(c, 'not yet valid');
    return;
  }
  if (c.startsWith('principal-type in ')) {
    if (!ctx.requesterPrincipalType) return; // Hub-trusted assertion
    const allowed = parseListBracket(c.slice('principal-type in '.length));
    if (!allowed.includes(ctx.requesterPrincipalType)) {
      throw new CaveatError(c, 'principal-type not in allowed set');
    }
    return;
  }
  if (c.startsWith('agent-id == ')) {
    if (!ctx.requesterAgentId) return;
    const want = c.slice('agent-id == '.length).trim();
    if (want !== ctx.requesterAgentId) throw new CaveatError(c, 'agent-id mismatch');
    return;
  }
  if (c.startsWith('device-id == ')) {
    if (!ctx.requesterDeviceId) return;
    const want = c.slice('device-id == '.length).trim();
    if (want !== ctx.requesterDeviceId) throw new CaveatError(c, 'device-id mismatch');
    return;
  }
  if (c.startsWith('operation in ')) {
    const allowed = parseListBracket(c.slice('operation in '.length));
    if (!allowed.includes(ctx.operation)) {
      throw new CaveatError(c, 'operation not allowed by caveat');
    }
    return;
  }
  if (c.startsWith('namespace == ')) {
    const want = c.slice('namespace == '.length).trim();
    if (want !== ctx.namespace) throw new CaveatError(c, 'namespace does not match');
    return;
  }
  if (c === 'nondelegatable') {
    if (ctx.isDelegationRequest) {
      throw new CaveatError(c, 'nondelegatable Grant cannot be used to mint a child Grant');
    }
    return;
  }
  if (c === 'idempotency-required') {
    if (!ctx.hasIdempotencyKey) {
      throw new CaveatError(c, 'X-Fabric-Idempotency-Key required');
    }
    return;
  }
  if (c.startsWith('rate <= ')) {
    const body = c.slice('rate <= '.length).trim();
    const parts = body.split(' per ');
    if (parts.length !== 2) throw new CaveatError(c, 'rate caveat must be `rate <= N per <unit>`');
    const n = parseInt(parts[0], 10);
    if (Number.isNaN(n) || n <= 0) throw new CaveatError(c, 'rate caveat N must be positive');
    const windowMs = windowToMs(parts[1].trim());
    if (windowMs == null) throw new CaveatError(c, 'rate caveat unit must be second|minute|hour|day');
    if (!ctx.rateConsume(ctx.grantId, n, windowMs)) {
      throw new CaveatError(c, 'rate limit exceeded', 'rate_limited');
    }
    return;
  }
  if (c.startsWith('max-amount <= ')) {
    if (!ctx.isBridgeCall) return;
    const body = c.slice('max-amount <= '.length).trim();
    const parts = body.split(/\s+/);
    if (parts.length !== 2) throw new CaveatError(c, 'max-amount must be `max-amount <= <int> <currency>`');
    const max = parseInt(parts[0], 10);
    if (Number.isNaN(max) || max <= 0) throw new CaveatError(c, 'max-amount integer invalid');
    if ((ctx.bridgeCurrency ?? '').toLowerCase() !== parts[1].toLowerCase()) {
      throw new CaveatError(c, 'currency does not match caveat');
    }
    if ((ctx.bridgeAmount ?? 0) > max) {
      throw new CaveatError(c, 'request amount exceeds max-amount');
    }
    return;
  }
  if (c.startsWith('only-domain in ')) {
    if (!ctx.isBridgeCall) return;
    if (!ctx.bridgeDomain) throw new CaveatError(c, 'bridge call missing domain');
    const allowed = parseListBracket(c.slice('only-domain in '.length)).map((s) => s.toLowerCase());
    const d = ctx.bridgeDomain.toLowerCase();
    if (!allowed.includes(d)) throw new CaveatError(c, 'domain not allowed by caveat');
    return;
  }
  if (c === 'requires-human-approval') {
    if (ctx.isBridgeCall) {
      throw new CaveatError(c, 'human approval required', 'human_approval');
    }
    return;
  }
  if (c.startsWith('discharge-from ')) {
    const want = c.slice('discharge-from '.length).trim();
    if (!want) throw new CaveatError(c, 'discharge-from caveat missing verifier url');
    if (ctx.dischargeIds.has(want)) return;
    if (ctx.dischargeLookup(want)) return;
    throw new CaveatError(c, 'missing discharge macaroon for ' + want);
  }
  throw new CaveatError(c, 'unknown caveat');
}

function windowToMs(unit: string): number | null {
  switch (unit) {
    case 'second': return 1_000;
    case 'minute': return 60_000;
    case 'hour':   return 3_600_000;
    case 'day':    return 86_400_000;
    default:       return null;
  }
}

function parseListBracket(s: string): string[] {
  s = s.trim();
  if (s.startsWith('[')) s = s.slice(1);
  if (s.endsWith(']')) s = s.slice(0, -1);
  return s.split(',').map((x) => x.trim()).filter(Boolean);
}
