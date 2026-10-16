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

  // A split series (proc.mem.footprint by role, turn.duration_ms by status)
  // reaches the chart as one row per sample, each carrying a single key: the
  // rows that share a timestamp are the roles measured at it. Folding a
  // bucket by part[0]'s keys dropped every other role and read the missing
  // ones as zero, so the default 24h chart of the series that shows a leak
  // drew 0 wherever the first role was not the bucket's last row.
  it('keeps every series of rows that share a timestamp', () => {
    const split: MetricRow[] = [
      { ts: 1000, self: 100 },
      { ts: 1000, child: 200 },
      { ts: 2000, self: 110 },
      { ts: 2000, child: 210 },
    ];
    expect(downsample(split, 1, 'last')).toEqual([
      { ts: 2000, self: 110, child: 210 },
    ]);
  });

  it('folds the rows of a timestamp into one point per bucket', () => {
    const split: MetricRow[] = [
      { ts: 1000, self: 100 },
      { ts: 1000, child: 200 },
      { ts: 1100, self: 105 },
      { ts: 1100, child: 205 },
      { ts: 2000, self: 110 },
      { ts: 2000, child: 210 },
      { ts: 2100, self: 115 },
      { ts: 2100, child: 215 },
    ];
    // chunk = ceil(8 / 2) = 4: two buckets of two timestamps each.
    expect(downsample(split, 2, 'last')).toEqual([
      { ts: 1100, self: 105, child: 205 },
      { ts: 2100, self: 115, child: 215 },
    ]);
  });

  it('leaves a series out of a bucket it has no sample in', () => {
    const split: MetricRow[] = [
      { ts: 1000, a: 10, b: 100 },
      { ts: 2000, a: 20 },
      { ts: 3000, b: 300 },
      { ts: 4000, a: 40, b: 400 },
    ];
    // A gap is not a measurement: b was measured once in the first bucket
    // (mean 100, not 50) and a once in the second (40, not 20).
    expect(downsample(split, 2, 'mean')).toEqual([
      { ts: 2000, a: 15, b: 100 },
      { ts: 4000, a: 40, b: 350 },
    ]);
    // A count is the same rule: nothing happened for the missing series.
    expect(downsample(split, 2, 'sum')).toEqual([
      { ts: 2000, a: 30, b: 100 },
      { ts: 4000, a: 40, b: 700 },
    ]);
  });
});
