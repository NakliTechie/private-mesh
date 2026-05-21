import { describe, it, expect } from 'vitest';
import { newNumericCode } from '../src/numeric-code.js';

describe('newNumericCode', () => {
  it('returns exactly n digits, zero-padded', () => {
    for (let i = 0; i < 200; i++) {
      const code = newNumericCode(6);
      expect(code).toHaveLength(6);
      expect(code).toMatch(/^\d{6}$/);
    }
  });

  it('rejects out-of-range n', () => {
    expect(() => newNumericCode(0)).toThrow(RangeError);
    expect(() => newNumericCode(-1)).toThrow(RangeError);
    expect(() => newNumericCode(10)).toThrow(RangeError);
  });

  it('produces enough variety to defeat trivial guessing', () => {
    // Regression for the prior Math.random() implementation: 1000 samples
    // should land in well over half the 6-digit space. Even a bad PRNG
    // would pass this — the assertion is just "is the RNG plugged in at all".
    const seen = new Set<string>();
    for (let i = 0; i < 1000; i++) seen.add(newNumericCode(6));
    expect(seen.size).toBeGreaterThan(950);
  });

  it('honors smaller n', () => {
    const code = newNumericCode(3);
    expect(code).toHaveLength(3);
    expect(code).toMatch(/^\d{3}$/);
  });
});
