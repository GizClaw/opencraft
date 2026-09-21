import { describe, expect, it } from 'vitest';
import { downsample, type MetricRow } from './metricSeries';

// Six samples in one bucket: the folding mode decides what the chart shows.
const rows: MetricRow[] = [3, 8, 2, 11, 5, 4].map((value, i) => ({
  ts: i * 1000,
  value,
}));

describe('downsample', () => {
  it('keeps a state series at the value it actually held', () => {
    // A running value (largest paint so far, DOM nodes) never dips to the
    // mean of the bucket; the last sample is what the page was at.
    expect(downsample(rows, 1, 'last')).toEqual([{ ts: 5000, value: 4 }]);
  });

  it('averages a per-event series', () => {
    expect(downsample(rows, 1, 'mean')).toEqual([{ ts: 5000, value: 33 / 6 }]);
  });

  it('keeps the worst value of the bucket', () => {
    expect(downsample(rows, 1, 'max')).toEqual([{ ts: 5000, value: 11 }]);
  });

  it('counts what happened in the bucket', () => {
    expect(downsample(rows, 1, 'sum')).toEqual([{ ts: 5000, value: 33 }]);
  });

  it('leaves a series that already fits untouched', () => {
    expect(downsample(rows, 400, 'mean')).toBe(rows);
  });

  it('folds every series of a multi-series row', () => {
    const split: MetricRow[] = [
      { ts: 0, ok: 1, failed: 5 },
      { ts: 1, ok: 3, failed: 2 },
    ];
    expect(downsample(split, 1, 'max')).toEqual([{ ts: 1, ok: 3, failed: 5 }]);
  });
});
