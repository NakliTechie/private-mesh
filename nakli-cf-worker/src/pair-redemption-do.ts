// Single-flight Durable Object that owns the "redeemed?" bit for one
// CRATE-PAIR token. Cloudflare KV has no atomic CAS, so the prior
// read-then-conditional-write flow let two concurrent redeemers BOTH
// mint a fresh daemon capability for the same pairing token. A
// Durable Object instance is single-threaded and durable per id, so
// addressing the DO by the token secret naturally serializes the
// redemption: the first call writes "redeemed=true"; the second sees
// the existing flag and returns already_redeemed.
//
// The DO holds the minimal slice of state needed for atomicity —
// {redeemed_by, redeemed_at}. The richer pairing-token record stays
// in KV (eventually-consistent read cache) so /pairing/intent/status
// reads from any edge stay cheap.

export interface RedemptionState {
  redeemed_by: string;
  redeemed_at: string; // ISO-8601
}

export interface RedeemResult {
  ok: boolean;
  // present on success — the recorded redemption time, in case the
  // caller wants to mirror it into KV.
  at?: string;
  // present on already-redeemed — the daemon that won the race and
  // the time it won.
  redeemed_by?: string;
  redeemed_at?: string;
}

// Stored under this key inside the DO's per-instance storage.
const STORAGE_KEY = 'redemption';

// PairRedemption is the Durable Object class. Wrangler binds one instance
// per token secret (via idFromName). The class is exported from
// src/worker.ts so the Cloudflare runtime can construct it.
export class PairRedemption {
  private state: DurableObjectState;

  constructor(state: DurableObjectState) {
    this.state = state;
  }

  // fetch is the DO's HTTP interface. The worker's redeem handler hits
  // POST / with a JSON body {daemon_pubkey}; the DO returns a JSON
  // RedeemResult. Status is 200 on success and 409 when already-redeemed
  // so the caller can branch without parsing the body.
  async fetch(req: Request): Promise<Response> {
    if (req.method !== 'POST') {
      return Response.json({ ok: false }, { status: 405 });
    }
    const body = (await req.json()) as { daemon_pubkey?: unknown };
    const daemonPubkey = typeof body?.daemon_pubkey === 'string' ? body.daemon_pubkey : '';
    if (!daemonPubkey) {
      return Response.json({ ok: false }, { status: 400 });
    }

    // The critical section. blockConcurrencyWhile serializes everything
    // happening on this DO instance during the closure, so any second
    // concurrent fetch() waits here until the first call's write has
    // committed and become visible to the read below.
    return await this.state.blockConcurrencyWhile(async () => {
      const existing = await this.state.storage.get<RedemptionState>(STORAGE_KEY);
      if (existing) {
        const out: RedeemResult = {
          ok: false,
          redeemed_by: existing.redeemed_by,
          redeemed_at: existing.redeemed_at,
        };
        return Response.json(out, { status: 409 });
      }
      const at = new Date().toISOString();
      const next: RedemptionState = { redeemed_by: daemonPubkey, redeemed_at: at };
      await this.state.storage.put(STORAGE_KEY, next);
      const out: RedeemResult = { ok: true, at };
      return Response.json(out, { status: 200 });
    });
  }
}

// tryRedeemViaDO is the worker-side helper that addresses the
// PairRedemption DO for a given token secret and forwards the redeem
// request. Returns null on success (caller may mint), or a populated
// RedeemResult indicating who won the race.
export async function tryRedeemViaDO(
  binding: DurableObjectNamespace,
  tokenSecret: string,
  daemonPubkey: string,
): Promise<{ at: string } | { redeemed_by: string; redeemed_at: string }> {
  const id = binding.idFromName(tokenSecret);
  const stub = binding.get(id);
  const resp = await stub.fetch('https://do/redeem', {
    method: 'POST',
    body: JSON.stringify({ daemon_pubkey: daemonPubkey }),
    headers: { 'content-type': 'application/json' },
  });
  const data = (await resp.json()) as RedeemResult;
  if (data.ok && data.at) {
    return { at: data.at };
  }
  return { redeemed_by: data.redeemed_by ?? '', redeemed_at: data.redeemed_at ?? '' };
}
