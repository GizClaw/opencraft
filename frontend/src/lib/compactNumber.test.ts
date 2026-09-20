import { describe, expect, it } from 'vitest';
import { formatCompact } from './compactNumber';

describe('formatCompact', () => {
  it('leaves small counts exact', () => {
    expect(formatCompact(0)).toBe('0');
    expect(formatCompact(999)).toBe('999');
    expect(formatCompact(12.34)).toBe('12.34');
  });

  it('keeps three significant digits per unit', () => {
    expect(formatCompact(1_234)).toBe('1.23k');
    expect(formatCompact(12_345)).toBe('12.3k');
    expect(formatCompact(123_456)).toBe('123k');
    expect(formatCompact(1_234_567)).toBe('1.23M');
    expect(formatCompact(12_345_678)).toBe('12.3M');
    expect(formatCompact(123_456_789)).toBe('123M');
  });

  it('climbs past millions instead of printing thousands of them', () => {
    // The usage tab hit this exact shape: 4.74 billion prompt tokens
    // came out as "4739.14M".
    expect(formatCompact(4_739_140_000)).toBe('4.74B');
    expect(formatCompact(47_391_400_000)).toBe('47.4B');
    expect(formatCompact(473_914_000_000)).toBe('474B');
    expect(formatCompact(4_739_140_000_000)).toBe('4.74T');
    expect(formatCompact(4_739_140_000_000_000)).toBe('4.74P');
    expect(formatCompact(4_739_140_000_000_000_000)).toBe('4.74E');
  });

  it('promotes a rounding carry into the next unit', () => {
    expect(formatCompact(999_999)).toBe('1.00M');
    expect(formatCompact(999_999_999)).toBe('1.00B');
  });

  it('stops at the last unit instead of inventing one', () => {
    expect(formatCompact(1e21)).toBe('1000E');
  });

  it('keeps the sign and drops trailing zeros on request', () => {
    expect(formatCompact(-2_500_000)).toBe('-2.50M');
    expect(formatCompact(-2_500_000, { trim: true })).toBe('-2.5M');
    expect(formatCompact(12_000, { trim: true })).toBe('12k');
    expect(formatCompact(1000, { trim: true })).toBe('1k');
  });

  it('never dresses a non-finite value as a unit', () => {
    expect(formatCompact(Number.NaN)).toBe('NaN');
    expect(formatCompact(Number.POSITIVE_INFINITY)).toBe('Infinity');
  });
});
