import { describe, expect, it } from 'vitest';
import { cacheHitPercent, formatHitPercent } from './usageRate';

describe('cacheHitPercent', () => {
  it('is a ratio when the prompt total includes the cached tokens', () => {
    // OpenAI shape: prompt_tokens 1000 of which 900 were cached.
    expect(cacheHitPercent({ input: 1000, cacheRead: 900 })).toBeCloseTo(90);
    // Anthropic shape after backend normalization: 200 uncached + 48000
    // read + 1024 written.
    expect(cacheHitPercent({ input: 49224, cacheRead: 48000 })).toBeCloseTo(
      97.51,
      1,
    );
  });

  it('reports zero for a prompt that had no cache hits', () => {
    expect(cacheHitPercent({ input: 5000, cacheRead: 0 })).toBe(0);
  });

  it('refuses a ratio a legacy row cannot support', () => {
    // Anthropic rows written before the input column became inclusive:
    // input_tokens holds the uncached 200 while the cache held 48000. The
    // old code clamped this to a confident 100%.
    expect(cacheHitPercent({ input: 200, cacheRead: 48000 })).toBeUndefined();
  });

  it('refuses a ratio when nothing was measured', () => {
    expect(cacheHitPercent({ input: 0, cacheRead: 0 })).toBeUndefined();
    expect(cacheHitPercent({ input: 0, cacheRead: 100 })).toBeUndefined();
    expect(cacheHitPercent({ input: -1, cacheRead: 10 })).toBeUndefined();
  });
});

describe('formatHitPercent', () => {
  it('keeps one decimal and drops it at the top of the range', () => {
    expect(formatHitPercent(97.53)).toBe('97.5');
    expect(formatHitPercent(99.96)).toBe('100');
    expect(formatHitPercent(0)).toBe('0.0');
    expect(formatHitPercent(undefined)).toBeUndefined();
  });
});
