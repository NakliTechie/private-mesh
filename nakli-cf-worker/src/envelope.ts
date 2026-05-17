// Wire-format response envelopes that mirror the Hub's
// (fabric-spec-001-v1.0.md §Wire format).

import { PROTOCOL_VERSION } from './env.js';

export interface Freshness {
  as_of: string;
  peers_synced: any[];
  peers_missing: any[];
  staleness_ms: number;
}

export function freshnessNow(): Freshness {
  return {
    as_of: new Date().toISOString(),
    peers_synced: [],
    peers_missing: [],
    staleness_ms: 0,
  };
}

const corsHeaders: Record<string, string> = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers':
    'Content-Type, X-Fabric-Grant, X-Fabric-Idempotency-Key, X-Fabric-Request-Id, ' +
    'X-Fabric-Version, X-Fabric-Agent-Id, X-Fabric-Device-Id, X-Fabric-Principal-Type, ' +
    'X-Fabric-Discharge',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Expose-Headers': 'X-Fabric-Version, X-Fabric-Request-Id',
};

function baseHeaders(extra: Record<string, string> = {}): Headers {
  const h = new Headers({
    'Content-Type': 'application/json',
    'X-Fabric-Version': PROTOCOL_VERSION,
    ...corsHeaders,
    ...extra,
  });
  return h;
}

export function corsResponse(): Response {
  return new Response(null, { status: 204, headers: baseHeaders() });
}

export function successResponse(
  data: unknown,
  freshness: Freshness | null = freshnessNow(),
  status = 200,
  extraHeaders: Record<string, string> = {},
): Response {
  const body = JSON.stringify({ ok: true, data, freshness });
  return new Response(body, { status, headers: baseHeaders(extraHeaders) });
}

export interface ErrorBody {
  code: ErrorCode;
  message: string;
  retryable: boolean;
}

export function errorResponse(
  code: ErrorCode,
  message: string,
  status: number,
  retryable = false,
  data?: unknown,
): Response {
  const env: Record<string, unknown> = {
    ok: false,
    error: { code, message, retryable },
  };
  if (data !== undefined) env.data = data;
  return new Response(JSON.stringify(env), { status, headers: baseHeaders() });
}

// Error codes catalogue — subset that the Worker emits.
export type ErrorCode =
  | 'grant_invalid'
  | 'grant_missing'
  | 'grant_revoked'
  | 'scope_denied'
  | 'caveat_unmet'
  | 'idempotency_conflict'
  | 'not_found'
  | 'conflict'
  | 'unavailable'
  | 'version_mismatch'
  | 'bad_request'
  | 'not_implemented'
  | 'rate_limited'
  | 'human_approval_required'
  | 'principal_retired';
