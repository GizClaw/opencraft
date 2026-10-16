// Both memos exist to make a pure computation run once per payload, not
// once per render; the failure that matters is therefore a *missing* hit
// (the work runs again) or a *wrong* hit (a stale answer). These cases pin
// the call counts and the invalidation edge — `undefined` results included,
// which is why the object memo tests for the key rather than the value.
import { describe, expect, it, vi } from 'vitest';
import { createLastValueMemo, createObjectMemo } from './memoStore';

describe('createObjectMemo', () => {
  it('computes once per object and again for a new object', () => {
    const compute = vi.fn((key: { n: number }) => key.n * 2);
    const memo = createObjectMemo(compute);
    const first = { n: 21 };
    expect(memo(first)).toBe(42);
    expect(memo(first)).toBe(42);
    expect(compute).toHaveBeenCalledTimes(1);
    expect(memo({ n: 2 })).toBe(4);
    expect(compute).toHaveBeenCalledTimes(2);
  });

  it('caches an undefined result instead of recomputing it', () => {
    const compute = vi.fn(() => undefined);
    const memo = createObjectMemo(compute);
    const key = {};
    expect(memo(key)).toBeUndefined();
    expect(memo(key)).toBeUndefined();
    expect(compute).toHaveBeenCalledTimes(1);
  });
});

describe('createLastValueMemo', () => {
  it('serves the same key from the last answer and recomputes a new one', () => {
    const compute = vi.fn((key: string) => key.length);
    const memo = createLastValueMemo(compute);
    expect(memo('abc')).toBe(3);
    expect(memo('abc')).toBe(3);
    expect(compute).toHaveBeenCalledTimes(1);
    expect(memo('abcd')).toBe(4);
    expect(compute).toHaveBeenCalledTimes(2);
  });

  it('recomputes when the key changes back', () => {
    const compute = vi.fn((key: string) => key);
    const memo = createLastValueMemo(compute);
    memo('a');
    memo('b');
    memo('a');
    expect(compute).toHaveBeenCalledTimes(3);
  });
});
