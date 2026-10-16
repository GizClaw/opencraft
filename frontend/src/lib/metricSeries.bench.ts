import { bench, describe } from 'vitest';
import { downsample, type MetricRow } from './metricSeries';

// F-53's bench: the charts downsample the probe's series on every read,
// so this runs the 5000-point x 3-role case the metrics view actually
// holds against the 500 points a chart draws.
describe('downsample', () => {
  const rows: MetricRow[] = Array.from({ length: 5000 }, (_, i) => ({
    ts: 1700000000000 + i * 1000,
    heap: 100 + (i % 50),
    goroutines: 40 + (i % 30),
    fps: 55 + (i % 10),
  }));

  for (const mode of ['last', 'mean', 'max', 'sum'] as const) {
    bench(`5000 points x 3 series to 500, ${mode}`, () => {
      downsample(rows, 500, mode);
    });
  }
});
