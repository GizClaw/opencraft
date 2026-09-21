// Downsampling for the metric charts. A chart in a wide range can hold
// thousands of samples per series, so points are folded into time-ordered
// buckets — but *how* they fold depends on what the series is.

// MetricAggregate is how one series folds inside a bucket.
//
//	last  a state that holds until it changes: a web-vitals running value
//	      (largest paint so far, worst interaction so far, cumulative
//	      layout shift), a gauge like DOM nodes or heap size. Averaging
//	      these invents values the page never had.
//	mean  a rate or a per-event measurement: frames per window, turn
//	      durations, stream-flush timings.
//	max   the worst value of the bucket: the largest frame gap or long
//	      task in that stretch of time.
//	sum   a count of things that happened in the bucket.
export type MetricAggregate = 'last' | 'mean' | 'max' | 'sum';

export interface MetricRow {
  ts: number;
  [series: string]: number;
}

function fold(values: number[], mode: MetricAggregate): number {
  switch (mode) {
    case 'last':
      return values[values.length - 1];
    case 'max':
      return Math.max(...values);
    case 'sum':
      return values.reduce((sum, value) => sum + value, 0);
    case 'mean':
      return values.reduce((sum, value) => sum + value, 0) / values.length;
  }
}

// downsample folds rows into at most maxPoints buckets, each carrying every
// series aggregated with the same mode.
export function downsample(
  rows: MetricRow[],
  maxPoints: number,
  mode: MetricAggregate,
): MetricRow[] {
  if (rows.length <= maxPoints || maxPoints <= 0) return rows;
  const chunk = Math.ceil(rows.length / maxPoints);
  const out: MetricRow[] = [];
  for (let i = 0; i < rows.length; i += chunk) {
    const part = rows.slice(i, i + chunk);
    const merged: MetricRow = { ts: part[part.length - 1].ts };
    for (const series of Object.keys(part[0])) {
      if (series === 'ts') continue;
      merged[series] = fold(
        part.map((row) => row[series] ?? 0),
        mode,
      );
    }
    out.push(merged);
  }
  return out;
}
