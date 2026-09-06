import { describe, expect, it } from 'vitest';
import { fillUsageSeries } from './UsageChart';
import type { UsagePoint } from '../lib/types';

function point(time: string, input: number): UsagePoint {
  return {
    time,
    input_tokens: input,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    reasoning_tokens: 0,
  };
}

describe('fillUsageSeries hour buckets', () => {
  it('fills every whole hour whose start lies in [start, end)', () => {
    const points = [
      point('2026-01-15T12:00:00Z', 120),
      point('2026-01-15T15:00:00Z', 60),
    ];
    const out = fillUsageSeries(
      points,
      'hour',
      Date.UTC(2026, 0, 15, 11, 45),
      Date.UTC(2026, 0, 15, 16, 15),
    );
    expect(out.map((p) => p.time)).toEqual([
      '2026-01-15T11:00:00Z',
      '2026-01-15T12:00:00Z',
      '2026-01-15T13:00:00Z',
      '2026-01-15T14:00:00Z',
      '2026-01-15T15:00:00Z',
      '2026-01-15T16:00:00Z',
    ]);
    expect(out[1].input_tokens).toBe(120);
    expect(out[4].input_tokens).toBe(60);
    expect(out[0].input_tokens).toBe(0);
    expect(out[5].input_tokens).toBe(0);
  });

  it('does not add a zero bucket for an exact whole-hour end', () => {
    const out = fillUsageSeries(
      [point('2026-01-15T12:00:00Z', 1)],
      'hour',
      Date.UTC(2026, 0, 15, 11, 0),
      Date.UTC(2026, 0, 15, 15, 0),
    );
    expect(out.map((p) => p.time)).toEqual([
      '2026-01-15T11:00:00Z',
      '2026-01-15T12:00:00Z',
      '2026-01-15T13:00:00Z',
      '2026-01-15T14:00:00Z',
    ]);
  });
});

describe('fillUsageSeries day buckets', () => {
  it('fills the local days containing start and end, exclusive aligned end', () => {
    const out = fillUsageSeries(
      [point('2026-01-15', 90)],
      'day',
      new Date(2026, 0, 15, 10, 30).getTime(),
      new Date(2026, 0, 17, 18, 0).getTime(),
    );
    expect(out.map((p) => p.time)).toEqual([
      '2026-01-15',
      '2026-01-16',
      '2026-01-17',
    ]);
    expect(out[0].input_tokens).toBe(90);
    expect(out[1].input_tokens).toBe(0);
    expect(out[2].input_tokens).toBe(0);
  });
});
