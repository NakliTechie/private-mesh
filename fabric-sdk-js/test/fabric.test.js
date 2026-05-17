// Unit tests for the new M5 surface. Uses a hand-rolled mock fetch to stand
// in for the Hub so these can run under Node without spawning a binary. The
// real-Hub end-to-end gate lives in scripts/js-gate.sh.

import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';

import { Fabric } from '../src/index.js';
import { bytesToBase64, base64ToBytes } from '../src/util/base64.js';
import {
  GrantMissingError, ScopeDeniedError, CaveatUnmetError, FabricError,
} from '../src/errors.js';

// Node 22+ exposes WebCrypto on globalThis already, but the SDK relies on
// `globalThis.crypto`, which `webcrypto` provides explicitly.
if (!globalThis.crypto) globalThis.crypto = webcrypto;

// ----- mock Hub -----
//
// A tiny in-memory store mirroring the Hub's /vault/append + /vault/stream
// surface. Enough to round-trip an event through the SDK.

function newMockHub() {
  const streams = new Map(); // key: ns/streamId, value: [eventOnTheWire]
  const seen = new Set();

  /** @returns {typeof fetch} */
  return {
    fetch: async (url, init) => {
      const u = new URL(url);
      const method = init?.method ?? 'GET';
      const path = u.pathname;
      const headers = new Headers(init?.headers ?? {});
      const body = init?.body ? JSON.parse(init.body) : null;

      const respond = (status, payload) => new Response(JSON.stringify(payload), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });

      if (path === '/fabric/v1/discover' && method === 'GET') {
        return respond(200, {
          ok: true,
          data: { transport_type: 'hub', version: 'naklimesh/1.0' },
          freshness: freshness(),
        });
      }
      if (path === '/fabric/v1/health' && method === 'GET') {
        return respond(200, {
          ok: true,
          data: { event_count: 0, degraded: false, degraded_reasons: [], peer_health: [] },
          freshness: freshness(),
        });
      }

      // Auth gate for everything else.
      if (!headers.get('X-Fabric-Grant')) {
        return respond(401, {
          ok: false,
          error: { code: 'grant_missing', message: 'X-Fabric-Grant header missing', retryable: false },
        });
      }

      if (path === '/fabric/v1/vault/append' && method === 'POST') {
        if (!headers.get('X-Fabric-Idempotency-Key')) {
          return respond(400, {
            ok: false,
            error: { code: 'bad_request', message: 'idempotency key required', retryable: false },
          });
        }
        const key = `${body.namespace}/${body.stream_id}`;
        const events = streams.get(key) ?? [];
        if (seen.has(headers.get('X-Fabric-Idempotency-Key'))) {
          const ev = events[events.length - 1];
          return respond(200, {
            ok: true,
            data: { event_id: ev.event_id, sequence_number: ev.sequence_number },
            freshness: freshness(),
          });
        }
        seen.add(headers.get('X-Fabric-Idempotency-Key'));
        const eventId = `evt-${events.length + 1}`;
        const seq = events.length + 1;
        const ev = {
          event_id: eventId,
          kind: body.event.kind,
          sequence_number: seq,
          payload_ciphertext: body.event.payload_ciphertext,
          causal_dependencies: body.event.causal_dependencies ?? [],
          vector_clock: body.event.vector_clock ?? {},
          appended_at: new Date().toISOString(),
          appended_by_principal: 'mock',
        };
        events.push(ev);
        streams.set(key, events);
        return respond(200, {
          ok: true,
          data: { event_id: eventId, sequence_number: seq },
          freshness: freshness(),
        });
      }

      if (method === 'GET' && path.startsWith('/fabric/v1/vault/stream/')) {
        // path: /fabric/v1/vault/stream/<ns>/<sid>
        const parts = path.split('/');
        const ns = decodeURIComponent(parts[5]);
        const sid = decodeURIComponent(parts[6]);
        const events = streams.get(`${ns}/${sid}`) ?? [];
        return respond(200, {
          ok: true,
          data: { events, more: false },
          freshness: freshness(),
        });
      }

      // Default: 501.
      return respond(501, {
        ok: false,
        error: { code: 'not_implemented', message: `mock has no handler for ${method} ${path}`, retryable: false },
      });
    },
  };

  function freshness() {
    return {
      as_of: new Date().toISOString(),
      peers_synced: [],
      peers_missing: [],
      staleness_ms: 0,
    };
  }
}

function randomSeed() {
  const s = new Uint8Array(32);
  globalThis.crypto.getRandomValues(s);
  return s;
}

// ----- tests -----

test('Fabric / discover round-trips', async () => {
  const hub = newMockHub();
  const f = new Fabric({
    transports: [{ url: 'http://hub.test', fetch: hub.fetch }],
  });
  const d = await f.discover();
  assert.equal(d.transport_type, 'hub');
  assert.equal(d.version, 'naklimesh/1.0');
});

test('Fabric / vault append+read round-trips with encryption', async () => {
  const hub = newMockHub();
  const f = new Fabric({
    transports: [{ url: 'http://hub.test', fetch: hub.fetch }],
  });
  f._useRootSeed(randomSeed());
  f.useGrant(fakeGrant());

  const payload = { item: 'milk', qty: 2 };
  const { eventId, sequenceNumber } = await f.vault.append({
    namespace: 'list',
    streamId: 'shopping',
    event: { kind: 'list:item-added', payload },
  });
  assert.equal(sequenceNumber, 1);
  assert.match(eventId, /^evt-/);

  const { events } = await f.vault.read('list', 'shopping');
  assert.equal(events.length, 1);
  assert.deepEqual(events[0].payload, payload);
});

test('Fabric / vault read with a different seed surfaces VaultDecryptionError', async () => {
  const hub = newMockHub();
  // Writer
  const writer = new Fabric({ transports: [{ url: 'http://hub.test', fetch: hub.fetch }] });
  writer._useRootSeed(randomSeed());
  writer.useGrant(fakeGrant());
  await writer.vault.append({
    namespace: 'list',
    streamId: 'rotated',
    event: { kind: 'k', payload: 'top secret' },
  });
  // Reader with the wrong seed
  const reader = new Fabric({ transports: [{ url: 'http://hub.test', fetch: hub.fetch }] });
  reader._useRootSeed(randomSeed());
  reader.useGrant(fakeGrant());
  const { events } = await reader.vault.read('list', 'rotated');
  assert.equal(events.length, 1);
  assert.equal(events[0].payload, undefined);
  assert.equal(events[0].decryptionError?.code, 'vault_decryption');
});

test('Fabric / vault append without a Grant raises GrantMissingError', async () => {
  const hub = newMockHub();
  const f = new Fabric({ transports: [{ url: 'http://hub.test', fetch: hub.fetch }] });
  f._useRootSeed(randomSeed());
  await assert.rejects(
    () => f.vault.append({
      namespace: 'list',
      streamId: 's',
      event: { kind: 'k', payload: 'x' },
    }),
    (e) => e instanceof GrantMissingError,
  );
});

test('Fabric / transport unreachable raises TransportUnavailableError', async () => {
  const f = new Fabric({
    transports: [{
      url: 'http://hub.test',
      fetch: async () => { throw new Error('ECONNREFUSED'); },
    }],
  });
  await assert.rejects(() => f.discover(), { code: 'unavailable' });
});

test('Fabric / freshness observer fires on response', async () => {
  const hub = newMockHub();
  const f = new Fabric({ transports: [{ url: 'http://hub.test', fetch: hub.fetch }] });
  f._useRootSeed(randomSeed());
  f.useGrant(fakeGrant());

  let observed = null;
  const unsubscribe = f.freshness.observe((snapshot) => { observed = snapshot; });
  await f.vault.append({
    namespace: 'list',
    streamId: 'fresh',
    event: { kind: 'k', payload: 'x' },
  });
  assert.ok(observed);
  assert.equal(observed.stalenessMs, 0);
  assert.equal(observed.withinBudget, true);
  unsubscribe();
});

test('Fabric / errors / fromEnvelope dispatches by code', async () => {
  // Force a 403 scope_denied via a custom mock.
  const f = new Fabric({
    transports: [{
      url: 'http://hub.test',
      fetch: async () => new Response(JSON.stringify({
        ok: false,
        error: { code: 'scope_denied', message: 'nope', retryable: false },
      }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
    }],
  });
  await assert.rejects(
    () => f.discover(),
    (e) => e instanceof ScopeDeniedError && e.code === 'scope_denied',
  );
  // CaveatUnmet
  const f2 = new Fabric({
    transports: [{
      url: 'http://hub.test',
      fetch: async () => new Response(JSON.stringify({
        ok: false,
        error: { code: 'caveat_unmet', message: 'no', retryable: false },
      }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
    }],
  });
  await assert.rejects(() => f2.discover(), (e) => e instanceof CaveatUnmetError);
  // generic FabricError fallthrough
  const f3 = new Fabric({
    transports: [{
      url: 'http://hub.test',
      fetch: async () => new Response(JSON.stringify({
        ok: false,
        error: { code: 'something_else', message: 'meh', retryable: false },
      }), { status: 418, headers: { 'Content-Type': 'application/json' } }),
    }],
  });
  await assert.rejects(() => f3.discover(), (e) => e instanceof FabricError && e.code === 'something_else');
});

test('Fabric / health.current surfaces degraded snapshot', async () => {
  const f = new Fabric({
    transports: [{
      url: 'http://hub.test',
      fetch: async () => new Response(JSON.stringify({
        ok: true,
        data: {
          event_count: 5,
          degraded: true,
          degraded_reasons: ['peer unreachable: http://1/x'],
          peer_health: [{ peer: 'http://1/x', reachable: false }],
        },
        freshness: { as_of: new Date().toISOString(), peers_synced: [], peers_missing: [], staleness_ms: 0 },
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    }],
  });
  const snap = await f.health.current();
  assert.equal(snap.overall, 'degraded');
  assert.deepEqual(snap.degradedReasons, ['peer unreachable: http://1/x']);
});

function fakeGrant() {
  // The mock Hub doesn't validate the macaroon; the byte string just needs to
  // be present so X-Fabric-Grant is set.
  return bytesToBase64(new TextEncoder().encode('mock-macaroon'));
}

// Keep base64ToBytes referenced so the import isn't flagged.
test('Fabric / base64 round-trip sanity', () => {
  const b = new TextEncoder().encode('hi');
  assert.deepEqual(base64ToBytes(bytesToBase64(b)), b);
});
