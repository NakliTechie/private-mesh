import { describe, it, expect } from 'vitest';
import { PairRedemption, RedeemResult } from '../src/pair-redemption-do.js';

// Minimal DurableObjectState stub. Storage is a plain Map.
// blockConcurrencyWhile is implemented with a real per-instance Promise
// chain so concurrent fetch() calls genuinely queue — this matches the
// production runtime's contract and lets the Promise.all test below
// actually prove serialization, not just code-path coverage.
function makeState(): DurableObjectState {
  const map = new Map<string, unknown>();
  let chain: Promise<unknown> = Promise.resolve();
  return {
    storage: {
      async get<T>(key: string): Promise<T | undefined> {
        return map.get(key) as T | undefined;
      },
      async put<T>(key: string, value: T): Promise<void> {
        map.set(key, value);
      },
    } as unknown as DurableObjectStorage,
    blockConcurrencyWhile: <T>(fn: () => Promise<T>): Promise<T> => {
      const next = chain.then(() => fn());
      // Catch on the chain so a thrown error in one fn doesn't poison
      // future calls; the awaited `next` still surfaces the rejection
      // to that caller.
      chain = next.catch(() => undefined);
      return next;
    },
  } as unknown as DurableObjectState;
}

async function redeem(do_: PairRedemption, daemonPubkey: string): Promise<{status: number; body: RedeemResult}> {
  const req = new Request('https://do/redeem', {
    method: 'POST',
    body: JSON.stringify({ daemon_pubkey: daemonPubkey }),
    headers: { 'content-type': 'application/json' },
  });
  const resp = await do_.fetch(req);
  return { status: resp.status, body: (await resp.json()) as RedeemResult };
}

describe('PairRedemption DO', () => {
  it('first redeem wins and records the daemon + timestamp', async () => {
    const state = makeState();
    const do_ = new PairRedemption(state);
    const { status, body } = await redeem(do_, 'daemon-A');
    expect(status).toBe(200);
    expect(body.ok).toBe(true);
    expect(body.at).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  });

  it('second redeem on the same instance returns 409 with the original redeemer', async () => {
    const state = makeState();
    const do_ = new PairRedemption(state);
    const first = await redeem(do_, 'daemon-A');
    const second = await redeem(do_, 'daemon-B');

    expect(first.status).toBe(200);
    expect(second.status).toBe(409);
    expect(second.body.ok).toBe(false);
    expect(second.body.redeemed_by).toBe('daemon-A');
    expect(second.body.redeemed_at).toBe(first.body.at);
  });

  it('rejects non-POST methods', async () => {
    const state = makeState();
    const do_ = new PairRedemption(state);
    const resp = await do_.fetch(new Request('https://do/redeem', { method: 'GET' }));
    expect(resp.status).toBe(405);
  });

  it('rejects missing daemon_pubkey', async () => {
    const state = makeState();
    const do_ = new PairRedemption(state);
    const resp = await do_.fetch(new Request('https://do/redeem', {
      method: 'POST',
      body: '{}',
      headers: { 'content-type': 'application/json' },
    }));
    expect(resp.status).toBe(400);
  });

  // The production runtime's blockConcurrencyWhile genuinely queues — the
  // stub above does not. This test still proves the storage-side guard
  // (read existing → bail if set) by issuing two truly-concurrent fetches
  // via Promise.all. In the stub, micro-task scheduling means the second
  // call's get() may observe the first call's put() — either way the
  // race ends with exactly one ok=true and exactly one ok=false.
  it('Promise.all over two concurrent redeems yields exactly one success', async () => {
    const state = makeState();
    const do_ = new PairRedemption(state);
    const [a, b] = await Promise.all([
      redeem(do_, 'daemon-A'),
      redeem(do_, 'daemon-B'),
    ]);
    const successes = [a, b].filter((r) => r.body.ok).length;
    const conflicts = [a, b].filter((r) => !r.body.ok).length;
    expect(successes).toBe(1);
    expect(conflicts).toBe(1);
  });
});
