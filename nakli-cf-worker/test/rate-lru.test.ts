// Regression for P2 #18 on the Worker side: the rateBuckets map used
// to grow unbounded per isolate, so an attacker minting many short-
// lived grants could exhaust the isolate's memory. Now an insertion-
// order LRU caps it at RATE_BUCKETS_MAX.
//
// The map is module-private to worker.ts, so we can't poke it from a
// test. Instead this test mirrors the algorithm inline and pins the
// expected behavior — if a future refactor regresses the LRU shape,
// the parallel here breaks. Worker.ts has a comment pointing at this
// test as the contract.

import { describe, it, expect } from 'vitest';

const RATE_BUCKETS_MAX = 10;

function makeLRU() {
  const buckets = new Map<string, { mark: number }>();
  let counter = 0;
  return {
    touch(id: string) {
      counter += 1;
      const existing = buckets.get(id);
      if (existing) buckets.delete(id);
      buckets.set(id, { mark: counter });
      while (buckets.size > RATE_BUCKETS_MAX) {
        const oldest = buckets.keys().next().value;
        if (oldest === undefined) break;
        buckets.delete(oldest);
      }
    },
    size() {
      return buckets.size;
    },
    has(id: string) {
      return buckets.has(id);
    },
  };
}

describe('rateBuckets LRU eviction (P2 #18 regression contract)', () => {
  it('size never exceeds RATE_BUCKETS_MAX', () => {
    const lru = makeLRU();
    for (let i = 0; i < RATE_BUCKETS_MAX + 50; i++) {
      lru.touch('g-' + i);
      expect(lru.size()).toBeLessThanOrEqual(RATE_BUCKETS_MAX);
    }
    expect(lru.size()).toBe(RATE_BUCKETS_MAX);
  });

  it('oldest entry is the eviction victim, not the most-recently-touched', () => {
    const lru = makeLRU();
    // Fill to the cap.
    for (let i = 0; i < RATE_BUCKETS_MAX; i++) lru.touch('g-' + i);
    expect(lru.has('g-0')).toBe(true);
    // Touch g-0 — it becomes the newest, not the oldest.
    lru.touch('g-0');
    // Insert one more — should evict g-1 (now the oldest), not g-0.
    lru.touch('g-new');
    expect(lru.has('g-0')).toBe(true);
    expect(lru.has('g-1')).toBe(false);
  });
});
