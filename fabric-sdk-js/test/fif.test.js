import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  FIFError,
  newFIF,
  parseFIF,
  newInnerFIF,
  RESERVED_ENVELOPE_TYPES,
  FIF_FORMAT,
  ENVELOPE_PASSPHRASE_ONLY,
} from '../src/identity/fif.js';
import { randomBytes } from '../src/crypto.js';
import { bytesToBase64 } from '../src/util/base64.js';

function sampleInner() {
  return newInnerFIF(
    {
      type: 'human',
      id: '01JFXAMPLETESTULID000001',
      display_name: 'Bhai',
      created_at: '2026-05-16T12:00:00Z',
    },
    {
      algorithm: 'ed25519',
      public_key: randomBytes(32),
      private_key: randomBytes(64),
    },
  );
}

describe('FIF round-trip', () => {
  it('newFIF → serialize → parseFIF → unlock → original inner', async () => {
    const inner = sampleInner();
    const fif = await newFIF('correct horse battery staple', inner);
    const bytes = fif.serialize();
    assert.ok(bytes.length > 0);

    const parsed = parseFIF(bytes);
    assert.equal(parsed.envelopeType, ENVELOPE_PASSPHRASE_ONLY);
    assert.equal(parsed.isUnlocked(), false);

    await parsed.unlock('correct horse battery staple');
    assert.equal(parsed.inner.principal.display_name, 'Bhai');
    assert.deepEqual(parsed.inner.root_keypair.public_key, inner.root_keypair.public_key);
    assert.deepEqual(parsed.inner.root_keypair.private_key, inner.root_keypair.private_key);
  });
});

describe('FIF authentication failures', () => {
  it('rejects wrong passphrase', async () => {
    const fif = await newFIF('right', sampleInner());
    const bytes = fif.serialize();
    const parsed = parseFIF(bytes);
    await assert.rejects(parsed.unlock('wrong'), (e) => {
      assert.ok(e instanceof FIFError);
      assert.equal(e.code, 'fif_auth');
      return true;
    });
  });

  it('rejects tampered header', async () => {
    const fif = await newFIF('pass', sampleInner());
    const bytes = fif.serialize();
    // Find the envelope_type marker in the on-wire header and mutate it.
    const text = new TextDecoder().decode(bytes);
    const idx = text.indexOf('passphrase-only');
    assert.ok(idx > 0, 'envelope_type marker must be present');
    bytes[idx] ^= 0x01;

    let parsed;
    try {
      parsed = parseFIF(bytes);
    } catch (e) {
      // Parse may reject because the mutated JSON no longer matches a known
      // envelope_type. That's an acceptable failure mode.
      assert.ok(e instanceof FIFError);
      assert.ok(['fif_format', 'fif_envelope_unsupported'].includes(e.code));
      return;
    }
    await assert.rejects(parsed.unlock('pass'), (e) => {
      assert.equal(e.code, 'fif_auth');
      return true;
    });
  });
});

describe('FIF envelope refusal (forward-compat hook)', () => {
  for (const et of RESERVED_ENVELOPE_TYPES) {
    it(`refuses reserved envelope_type "${et}" with fif_envelope_unsupported`, () => {
      const bytes = headerOnlyFIF(et);
      assert.throws(
        () => parseFIF(bytes),
        (e) => {
          assert.equal(e.code, 'fif_envelope_unsupported');
          return true;
        },
      );
    });
  }
  it('refuses unknown envelope_type', () => {
    const bytes = headerOnlyFIF('bogus-envelope');
    assert.throws(
      () => parseFIF(bytes),
      (e) => {
        assert.equal(e.code, 'fif_envelope_unsupported');
        return true;
      },
    );
  });
  it('refuses unknown format', () => {
    const bytes = headerOnlyFIF(ENVELOPE_PASSPHRASE_ONLY, 'fif/9.9');
    assert.throws(
      () => parseFIF(bytes),
      (e) => {
        assert.equal(e.code, 'fif_format');
        return true;
      },
    );
  });
});

describe('FIF nonce freshness (regression for AEAD nonce reuse)', () => {
  // Prior versions stored a single nonce at newFIF time and reused it on
  // every serialize. Two snapshots under the same (key, nonce) leak the
  // keystream and the Poly1305 MAC key.
  it('emits a fresh nonce on every serialize, no mutation', async () => {
    const fif = await newFIF('pass', sampleInner());
    const bytes1 = fif.serialize();
    const parsed1 = parseFIF(bytes1);
    const nonce1 = parsed1.nonce;

    const bytes2 = fif.serialize();
    const parsed2 = parseFIF(bytes2);
    const nonce2 = parsed2.nonce;

    assert.notDeepEqual(nonce1, nonce2, 'nonce reused across serialize calls');
    await parsed1.unlock('pass');
    await parsed2.unlock('pass');
  });

  it('emits a fresh nonce after inner mutation, both snapshots decrypt', async () => {
    const fif = await newFIF('pass', sampleInner());
    const bytes1 = fif.serialize();

    // Enrol a device subkey — the realistic mutation case.
    fif.inner.device_subkeys.push({
      device_id: '01JDEVICETESTULID0000001',
      device_name: 'phone',
      algorithm: 'ed25519',
      public_key: randomBytes(32),
      private_key: randomBytes(64),
      enrolled_at: '2026-05-21T00:00:00Z',
    });

    const bytes2 = fif.serialize();
    const parsed1 = parseFIF(bytes1);
    const parsed2 = parseFIF(bytes2);
    assert.notDeepEqual(parsed1.nonce, parsed2.nonce, 'nonce reused after inner mutation');

    await parsed1.unlock('pass');
    await parsed2.unlock('pass');
    assert.equal(parsed1.inner.device_subkeys.length, 0);
    assert.equal(parsed2.inner.device_subkeys.length, 1);
  });
});

describe('FIF lock', () => {
  it('clears inner and forbids serialize', async () => {
    const fif = await newFIF('pass', sampleInner());
    assert.equal(fif.isUnlocked(), true);
    fif.lock();
    assert.equal(fif.isUnlocked(), false);
    assert.throws(() => fif.serialize(), (e) => e.code === 'identity_locked');
  });
});

/** Build a syntactically valid FIF with the given envelope_type and format. */
function headerOnlyFIF(envelopeType, format = FIF_FORMAT) {
  const header = {
    format,
    envelope_type: envelopeType,
    envelope_params: {
      kdf: 'argon2id',
      kdf_params: { m_cost: 65536, t_cost: 3, parallelism: 4 },
      salt: bytesToBase64(new Uint8Array(16).fill(1)),
      nonce: bytesToBase64(new Uint8Array(24).fill(2)),
    },
  };
  const hdrJSON = new TextEncoder().encode(JSON.stringify(header));
  const body = new Uint8Array(32); // junk; parse should fail before decrypt
  const out = new Uint8Array(4 + hdrJSON.length + body.length);
  new DataView(out.buffer).setUint32(0, hdrJSON.length, false);
  out.set(hdrJSON, 4);
  out.set(body, 4 + hdrJSON.length);
  return out;
}
