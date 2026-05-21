import { describe, it, expect } from 'vitest';
import { readBodyCapped, requestBodyLimitBytes, BodyTooLargeError } from '../src/body-cap.js';

function reqWith(body: Uint8Array | null, contentLength?: number | 'omit'): Request {
  const init: RequestInit = body !== null ? { method: 'POST', body } : { method: 'POST' };
  const req = new Request('https://example.invalid/x', init);
  // Override or strip content-length depending on the test scenario.
  if (contentLength === 'omit') {
    // Build a fresh Request without content-length.
    const stream = body !== null ? new Blob([body]).stream() : null;
    return new Request('https://example.invalid/x', {
      method: 'POST',
      body: stream as any,
      duplex: 'half',
    } as any);
  }
  if (typeof contentLength === 'number') {
    req.headers.set('content-length', String(contentLength));
  }
  return req;
}

describe('readBodyCapped', () => {
  it('returns the body when under the cap', async () => {
    const data = new Uint8Array([1, 2, 3, 4, 5]);
    const out = await readBodyCapped(reqWith(data), 1024);
    expect(Array.from(out)).toEqual([1, 2, 3, 4, 5]);
  });

  it('rejects early when content-length exceeds cap', async () => {
    const data = new Uint8Array(10);
    const req = reqWith(data, 1_000_000);
    await expect(readBodyCapped(req, 1024)).rejects.toBeInstanceOf(BodyTooLargeError);
  });

  it('rejects mid-stream when actual bytes exceed cap (lying content-length)', async () => {
    const big = new Uint8Array(8192);
    // Set a small content-length so the pre-check passes, but real body is bigger.
    const req = reqWith(big, 8);
    await expect(readBodyCapped(req, 16)).rejects.toBeInstanceOf(BodyTooLargeError);
  });

  it('returns empty array when body is empty', async () => {
    const out = await readBodyCapped(reqWith(new Uint8Array(0)), 1024);
    expect(out.byteLength).toBe(0);
  });
});

describe('requestBodyLimitBytes', () => {
  it('defaults to 2 MiB + 256 KiB when env is unset', () => {
    const got = requestBodyLimitBytes({});
    expect(got).toBe((1 << 20) * 2 + (256 << 10));
  });

  it('honors MAX_EVENT_SIZE_BYTES from env', () => {
    const got = requestBodyLimitBytes({ MAX_EVENT_SIZE_BYTES: '524288' }); // 512 KiB
    expect(got).toBe(524288 * 2 + (256 << 10));
  });

  it('falls back when MAX_EVENT_SIZE_BYTES is bogus', () => {
    const got = requestBodyLimitBytes({ MAX_EVENT_SIZE_BYTES: 'not-a-number' });
    expect(got).toBe((1 << 20) * 2 + (256 << 10));
  });
});
