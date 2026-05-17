// M5 ships Sync, LLM, and Bridge APIs as thin call-through stubs that hit the
// Hub's existing endpoints (most of which return 501 in v1.0). Full handling
// — browser-local inference for LLM, adapter framework for Bridge, multi-
// anchor sync — lands at M5.5 / M7.

import { newIdempotencyKey } from './transport.js';
import { HumanApprovalRequiredError } from './errors.js';

export class SyncAPI {
  constructor(opts) {
    this._transports = opts.transports;
    this._getCurrentGrant = opts.getCurrentGrant;
  }
  async status() {
    return { peers: [], overallFreshnessMs: 0 };
  }
  async peers() {
    const t = this._transports.pick();
    const res = await t.do('GET', '/fabric/v1/sync/peers', { grant: this._getCurrentGrant() });
    return res.data?.peers ?? [];
  }
  async forcePull() { throw new Error('sync.forcePull: ships at M7 (multi-anchor)'); }
  async forcePush() { throw new Error('sync.forcePush: ships at M7 (multi-anchor)'); }
}

export class LLMAPI {
  constructor(opts) {
    this._transports = opts.transports;
    this._getCurrentGrant = opts.getCurrentGrant;
    this._backends = new Map();
  }
  async routes() {
    const t = this._transports.pick();
    const res = await t.do('GET', '/fabric/v1/llm/routes', { grant: this._getCurrentGrant() });
    return res.data?.routes ?? [];
  }
  async complete(spec) {
    if (spec?.preferredRoute === 'browser-local') {
      const backend = this._backends.values().next().value;
      if (!backend) throw new Error('llm.complete: no browser-local backend registered');
      return backend.generate(spec.messages, spec);
    }
    // Hub returns 501 for remote LLM in v1.0.
    const t = this._transports.pick();
    const res = await t.do('POST', '/fabric/v1/llm/complete', {
      body: spec,
      grant: this._getCurrentGrant(),
      idempotencyKey: spec.idempotencyKey ?? newIdempotencyKey(),
    });
    return res.data;
  }
  registerBrowserBackend(backend) {
    this._backends.set(backend.name, backend);
  }
}

export class BridgeAPI {
  constructor(opts) {
    this._transports = opts.transports;
    this._getCurrentGrant = opts.getCurrentGrant;
  }
  async adapters() {
    const t = this._transports.pick();
    const res = await t.do('GET', '/fabric/v1/bridge/adapters', { grant: this._getCurrentGrant() });
    return res.data?.adapters ?? [];
  }
  async call(spec) {
    const t = this._transports.pick();
    try {
      const res = await t.do('POST', '/fabric/v1/bridge/call', {
        body: {
          adapter: spec.adapter,
          operation: spec.operation,
          domain: spec.domain,
          amount: spec.amount,
          currency: spec.currency,
          params: spec.params ?? {},
        },
        grant: spec.grant ?? this._getCurrentGrant(),
        idempotencyKey: spec.idempotencyKey ?? newIdempotencyKey(),
      });
      return { status: 'completed', result: res.data };
    } catch (e) {
      if (e instanceof HumanApprovalRequiredError) {
        return { status: 'pending', pendingOperationId: e.pendingOperationId };
      }
      throw e;
    }
  }
  async approve(pendingOperationId) {
    const t = this._transports.pick();
    const res = await t.do('POST', '/fabric/v1/bridge/approve', {
      body: { pending_id: pendingOperationId },
      grant: this._getCurrentGrant(),
    });
    return res.data;
  }
  async listPending() {
    return [];
  }
}
