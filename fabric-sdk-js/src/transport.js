// HTTP transport for talking to a Hub. The SDK exposes TransportManager as
// `fabric.transports` (spec §Transport management); the actual selection logic
// is single-Hub in M5 — fallback / queue / SSE land in M5.x.

import { ulid } from 'ulidx';

import { fromEnvelope, FabricError, TransportUnavailableError } from './errors.js';

/** PROTOCOL_VERSION is sent in X-Fabric-Version on every request. */
export const PROTOCOL_VERSION = 'naklimesh/1.0';

/**
 * A single Hub transport (the only type currently supported by the JS SDK in
 * M5). Wraps fetch with the protocol's headers and envelope shape.
 */
export class HubTransport {
  /**
   * @param {{ id?: string, url: string, fetch?: typeof fetch, timeoutMs?: number }} cfg
   */
  constructor(cfg) {
    if (!cfg?.url) throw new Error('HubTransport: url is required');
    this.id = cfg.id ?? 'hub';
    this.type = 'hub';
    this.url = cfg.url.replace(/\/+$/, '');
    this.preference = cfg.preference ?? 1;
    this._fetch = cfg.fetch ?? globalThis.fetch?.bind(globalThis);
    this._timeoutMs = cfg.timeoutMs ?? 30_000;
    if (!this._fetch) {
      throw new Error('HubTransport: fetch is not available; pass cfg.fetch');
    }
  }

  /**
   * Issue a request and parse the envelope.
   * @param {string} method
   * @param {string} path     starts with "/fabric/v1/..."
   * @param {{ body?: any, grant?: string, idempotencyKey?: string, headers?: Record<string,string>, discharges?: string[] }} [opts]
   * @returns {Promise<{ data: any, response: Response, raw: any }>}
   */
  async do(method, path, opts = {}) {
    const headers = {
      'X-Fabric-Version': PROTOCOL_VERSION,
      ...(opts.headers ?? {}),
    };
    if (opts.body != null) headers['Content-Type'] = 'application/json';
    if (opts.grant) headers['X-Fabric-Grant'] = opts.grant;
    if (opts.idempotencyKey) headers['X-Fabric-Idempotency-Key'] = opts.idempotencyKey;
    if (opts.discharges?.length) headers['X-Fabric-Discharge'] = opts.discharges.join(', ');

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this._timeoutMs);
    let response;
    try {
      response = await this._fetch(this.url + path, {
        method,
        headers,
        body: opts.body == null ? undefined : JSON.stringify(opts.body),
        signal: controller.signal,
      });
    } catch (e) {
      throw new TransportUnavailableError(
        `transport ${this.id}: ${method} ${path} failed: ${e?.message ?? e}`,
        { cause: e },
      );
    } finally {
      clearTimeout(timer);
    }

    let raw = null;
    const text = await response.text();
    if (text && response.headers.get('content-type')?.includes('json')) {
      try {
        raw = JSON.parse(text);
      } catch (e) {
        throw new FabricError(`transport: bad JSON in response: ${e.message}`, { code: 'bad_response' });
      }
    }
    if (!response.ok || raw?.ok === false) {
      throw fromEnvelope(raw, response.status);
    }
    return { data: raw?.data, freshness: raw?.freshness, response, raw };
  }
}

/**
 * Manages one or more transports. Spec §Transport management.
 */
export class TransportManager {
  /**
   * @param {{ transports?: HubTransport[] }} [opts]
   */
  constructor(opts = {}) {
    /** @type {HubTransport[]} */
    this._transports = [...(opts.transports ?? [])];
    this._current = this._transports[0] ?? null;
  }

  list() {
    return this._transports.map((t) => ({
      id: t.id,
      type: t.type,
      url: t.url,
      preference: t.preference,
    }));
  }

  add(transport) {
    this._transports.push(transport);
    this._transports.sort((a, b) => a.preference - b.preference);
    if (!this._current) this._current = transport;
  }

  remove(transportId) {
    this._transports = this._transports.filter((t) => t.id !== transportId);
    if (this._current?.id === transportId) {
      this._current = this._transports[0] ?? null;
    }
  }

  current() {
    return this._current;
  }

  switch(transportId) {
    const t = this._transports.find((x) => x.id === transportId);
    if (!t) throw new Error(`transport ${transportId} not registered`);
    this._current = t;
  }

  /** Pick the current transport for a request. M5 = single-transport. */
  pick() {
    if (!this._current) {
      throw new TransportUnavailableError('no transport configured');
    }
    return this._current;
  }
}

/** newIdempotencyKey returns a ULID suitable for X-Fabric-Idempotency-Key. */
export function newIdempotencyKey() {
  return ulid();
}
