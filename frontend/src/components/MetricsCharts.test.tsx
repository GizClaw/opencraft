// Regression: a split metric (proc.mem.footprint by role, turn.duration_ms
// by status) arrives as one sample per (ts, role). The panel used to hand
// the downsampler one row per sample, so a bucket knew only the key of its
// first row and filled every other role with 0 — the default 24h view of
// the series that is supposed to show a leaking renderer drew 0, or broke
// the line, for every role but the first.
//
// The other half of the same finding: the series were rebuilt in the render
// body, so the auto-refresh's busy flag (which re-renders the panel before
// and after every fetch) rebuilt all ~30 charts — 5000 points each at
// range=all — twice per refresh, inside the very view the perf probe
// measures.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import i18n from '../i18n';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MetricsCharts } from './MetricsCharts';
import type { MetricRow } from '../lib/metricSeries';

const diagnosticsMock = vi.hoisted(() => ({ MetricRange: vi.fn() }));

vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => diagnosticsMock,
);

// The fold itself is the thing under test, so the spy wraps the real one:
// the tests count how often a batch of points becomes chart rows.
const seriesMock = vi.hoisted(() => ({ downsample: vi.fn() }));

vi.mock('../lib/metricSeries', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/metricSeries')>();
  seriesMock.downsample.mockImplementation(actual.downsample);
  return { ...actual, downsample: seriesMock.downsample };
});

interface Point {
  ts: number;
  value: number;
  attrs?: Record<string, string>;
}

// proc.mem.footprint as the backend samples it: every process role measured
// at one snapshot carries the same timestamp.
function footprint(timestamps: number): Point[] {
  const points: Point[] = [];
  for (let i = 0; i < timestamps; i++) {
    const ts = 1000 + i * 1000;
    points.push({ ts, value: 100 + i, attrs: { role: 'self' } });
    points.push({ ts, value: 200 + i, attrs: { role: 'child' } });
    points.push({ ts, value: 300 + i, attrs: { role: 'gpu' } });
  }
  return points;
}

// The calls the panel made with a non-empty series, in METRICS order.
function filledCalls(): MetricRow[][] {
  return seriesMock.downsample.mock.calls
    .map(([rows]) => rows as MetricRow[])
    .filter((rows) => rows.length > 0);
}

beforeEach(() => {
  vi.clearAllMocks();
  diagnosticsMock.MetricRange.mockImplementation(async (name: string) =>
    name === 'proc.mem.footprint' ? footprint(3) : [],
  );
});

afterEach(() => {
  vi.useRealTimers();
});

describe('MetricsCharts', () => {
  it('charts one row per timestamp, carrying every role of it', async () => {
    render(<MetricsCharts />);
    expect(await screen.findByText('proc.mem.footprint')).toBeInTheDocument();

    const calls = filledCalls();
    expect(calls).toHaveLength(1);
    expect(calls[0]).toEqual([
      { ts: 1000, self: 100, child: 200, gpu: 300 },
      { ts: 2000, self: 101, child: 201, gpu: 301 },
      { ts: 3000, self: 102, child: 202, gpu: 302 },
    ]);
  });

  it('keeps every role of a downsampled bucket', async () => {
    // More timestamps than the 400-point cap, so the fold actually runs.
    diagnosticsMock.MetricRange.mockImplementation(async (name: string) =>
      name === 'proc.mem.footprint' ? footprint(500) : [],
    );

    render(<MetricsCharts />);
    await screen.findByText('proc.mem.footprint');

    const call = seriesMock.downsample.mock.calls.findIndex(
      ([rows]) => (rows as MetricRow[]).length > 0,
    );
    expect(call).toBeGreaterThanOrEqual(0);
    const folded = seriesMock.downsample.mock.results[call]
      .value as MetricRow[];

    expect(folded.length).toBeLessThanOrEqual(400);
    expect(folded.length).toBeGreaterThan(0);
    for (const row of folded) {
      // A bucket carries all three roles — a line that vanishes or drops to
      // 0 is what the leak chart must never draw — and each one is the last
      // value the series held in the bucket.
      expect(Object.keys(row).sort()).toEqual(['child', 'gpu', 'self', 'ts']);
      expect(row.self).toBeGreaterThan(0);
      expect(row.child).toBeGreaterThan(0);
      expect(row.gpu).toBeGreaterThan(0);
    }
  });

  it('keeps the spinner with the newest refresh', async () => {
    // Two batches, both held open: the panel must still be spinning after
    // the older one answers, because the fetch the reader is waiting for
    // is the other one.
    // One resolver per call: a batch is every metric the panel asked for,
    // and the `Promise.all` behind it settles only when all have answered.
    const batches: ((points: Point[]) => void)[][] = [];
    let batch = 0;
    diagnosticsMock.MetricRange.mockImplementation(
      () =>
        new Promise<Point[]>((resolve) => {
          (batches[batch] ??= []).push(resolve);
        }),
    );

    render(<MetricsCharts />);
    // The panel holds every chart behind the first load, so the title is
    // no landmark here: wait for the first batch to be issued instead.
    await waitFor(() => expect(batches[0]?.length).toBeGreaterThan(0));
    const spinner = () => document.querySelector('.animate-spin');
    expect(spinner()).not.toBeNull();

    batch = 1;
    fireEvent.click(
      screen.getByRole('button', { name: i18n.t('config.metricsRefresh') }),
    );

    // The first batch answers last, and its `finally` was what ended the
    // newer fetch's spinner early.
    await act(async () => {
      for (const resolve of batches[0] ?? []) resolve(footprint(1));
    });
    expect(spinner()).not.toBeNull();

    await act(async () => {
      for (const resolve of batches[1] ?? []) resolve(footprint(2));
    });
    expect(spinner()).toBeNull();
  });

  it('rebuilds the series per batch of points, not per render', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    render(<MetricsCharts />);
    await screen.findByText('proc.mem.footprint');

    const fetches = diagnosticsMock.MetricRange.mock.calls.length;
    const builds = seriesMock.downsample.mock.calls.length;
    expect(fetches).toBeGreaterThan(0);
    expect(builds).toBeGreaterThan(0);

    // The auto-refresh sets busy while its fetch is still out, which is a
    // full re-render with the same points in hand.
    diagnosticsMock.MetricRange.mockImplementation(() => new Promise(() => {}));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });

    expect(diagnosticsMock.MetricRange.mock.calls.length).toBeGreaterThan(
      fetches,
    );
    expect(seriesMock.downsample.mock.calls.length).toBe(builds);
  });
});
