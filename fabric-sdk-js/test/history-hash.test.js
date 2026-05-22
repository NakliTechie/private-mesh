import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { computeHistoryEventHash, HistoryHashError } from '../src/history.js';

// Convert a Uint8Array to lowercase hex.
function bytesToHex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

describe('History event_hash cross-SDK fixture (P2 #17)', () => {
  // This hex is the output of the Hub's storage.ComputeHistoryEventHash
  // when called with the same canonical inputs below. Captured once
  // from a Go fixture (see commit message); both implementations MUST
  // produce the same digest for the same input. If this assertion ever
  // breaks, the SDKs have drifted — surface the divergence loudly
  // rather than silently desync the audit log.
  const expectedHex = 'ed82acf30f04e3b89d345e7a6cac1a9b85e2d3d6ba5008ecb1c29f24be1fa878';

  it('matches Go reference for genesis event (empty prev + empty deps)', async () => {
    const got = await computeHistoryEventHash(
      new Uint8Array(0),       // prev — genesis event
      '01JEVTHASHFIXTURE0000000001',
      'test/event',
      '',                       // payload_metadata
      [],                       // causal_dependencies → '[]' JSON
    );
    assert.equal(bytesToHex(got), expectedHex);
  });

  it('produces a different hash when prev changes (chain integrity)', async () => {
    const a = await computeHistoryEventHash(new Uint8Array(0), 'eid', 'k', '', []);
    const b = await computeHistoryEventHash(new Uint8Array([1, 2, 3]), 'eid', 'k', '', []);
    assert.notDeepEqual(a, b);
  });

  it('produces a different hash when event_id changes', async () => {
    const a = await computeHistoryEventHash(new Uint8Array(0), 'eid-A', 'k', '', []);
    const b = await computeHistoryEventHash(new Uint8Array(0), 'eid-B', 'k', '', []);
    assert.notDeepEqual(a, b);
  });

  it('produces a different hash when kind changes', async () => {
    const a = await computeHistoryEventHash(new Uint8Array(0), 'eid', 'kind-A', '', []);
    const b = await computeHistoryEventHash(new Uint8Array(0), 'eid', 'kind-B', '', []);
    assert.notDeepEqual(a, b);
  });

  it('causal_deps order is preserved (JSON.stringify order-sensitive)', async () => {
    const a = await computeHistoryEventHash(new Uint8Array(0), 'eid', 'k', '', ['A', 'B']);
    const b = await computeHistoryEventHash(new Uint8Array(0), 'eid', 'k', '', ['B', 'A']);
    assert.notDeepEqual(a, b);
  });
});

describe('HistoryHashError shape', () => {
  it('carries expected and got fields', () => {
    const e = new HistoryHashError('mismatch', 'aa', 'bb');
    assert.equal(e.name, 'HistoryHashError');
    assert.equal(e.expected, 'aa');
    assert.equal(e.got, 'bb');
  });
});
