import { describe, expect, it } from 'vitest';
import {
  formatBytes,
  formatDurationMs,
  formatMetricSample,
} from './metricFormat';

describe('formatDurationMs', () => {
  it('climbs the time ladder instead of the decimal one', () => {
    expect(formatDurationMs(42)).toBe('42 ms');
    expect(formatDurationMs(1500)).toBe('1.5 s');
    expect(formatDurationMs(90_000)).toBe('1.5 min');
    expect(formatDurationMs(3_800_000)).toBe('1.1 h');
  });
});

describe('formatMetricSample', () => {
  it('folds the unit into the value where the scale changes', () => {
    // The bug this pins: a millisecond series past a million used to print
    // "3.80M ms" — a compact number carrying the raw unit.
    expect(formatMetricSample(3_800_000, 'ms')).toBe('1.1 h');
    expect(formatMetricSample(2048, 'B')).toBe('2.0 KB');
  });

  it('keeps a bare number for counts and appends real units', () => {
    expect(formatMetricSample(1_250_000, '')).toBe('1.25M');
    expect(formatMetricSample(12, '%')).toBe('12 %');
  });
});

describe('formatBytes', () => {
  it('scales from bytes to gigabytes', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1024 ** 3 * 1.5)).toBe('1.5 GB');
  });
});
