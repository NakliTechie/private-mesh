import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  mint,
  parse,
  verifySignature,
  alwaysSatisfied,
  SignatureInvalidError,
} from '../src/grant/macaroon.js';
import { randomBytes } from '../src/crypto.js';

function sampleSpec(rootKey) {
  return {
    rootKey,
    location: 'https://hub.bhai.example',
    identifier: {
      grant_id: '01JFXAMPLETESTGRANT00001',
      issued_at: '2026-05-16T12:00:00Z',
      issued_by_principal: '01JFXAMPLEHUMAN0000000001',
      issued_by_keypair: randomBytes(32),
      scope: {
        primitive: 'vault',
        namespace: 'list',
        operations: ['read', 'write'],
      },
    },
    caveats: ['time < 2026-06-15T18:00:00Z', 'operation in [read, write]'],
  };
}

describe('Macaroon mint + verify', () => {
  it('round-trips signature', () => {
    const rk = randomBytes(32);
    const g = mint(sampleSpec(rk));
    assert.ok(g.macaroon.length > 0);
    assert.equal(g.grantId, '01JFXAMPLETESTGRANT00001');
    verifySignature(g.macaroon, rk, alwaysSatisfied);
  });

  it('parse preserves identifier and caveats', () => {
    const rk = randomBytes(32);
    const spec = sampleSpec(rk);
    const g = mint(spec);
    const parsed = parse(g.macaroon);
    assert.equal(parsed.identifier.grant_id, spec.identifier.grant_id);
    assert.equal(parsed.identifier.scope.primitive, 'vault');
    assert.deepEqual(parsed.identifier.issued_by_keypair, spec.identifier.issued_by_keypair);
    assert.equal(parsed.caveats.length, spec.caveats.length);
    for (let i = 0; i < spec.caveats.length; i++) {
      assert.equal(parsed.caveats[i], spec.caveats[i]);
    }
  });

  it('rejects wrong root key', () => {
    const rk1 = randomBytes(32);
    const rk2 = randomBytes(32);
    const g = mint(sampleSpec(rk1));
    assert.throws(() => verifySignature(g.macaroon, rk2, alwaysSatisfied), SignatureInvalidError);
  });

  it('rejects tampered macaroon', () => {
    const rk = randomBytes(32);
    const g = mint(sampleSpec(rk));
    g.macaroon[Math.floor(g.macaroon.length / 2)] ^= 0x01;
    assert.throws(() => verifySignature(g.macaroon, rk, alwaysSatisfied));
  });

  it('check function can reject', () => {
    const rk = randomBytes(32);
    const g = mint(sampleSpec(rk));
    assert.throws(
      () =>
        verifySignature(g.macaroon, rk, (c) => {
          throw new Error('rejected: ' + c);
        }),
      (err) => {
        assert.ok(err instanceof SignatureInvalidError);
        assert.match(String(err.cause?.message ?? ''), /rejected:/);
        return true;
      },
    );
  });
});
