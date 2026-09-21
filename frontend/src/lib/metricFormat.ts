import { formatCompact } from './compactNumber';

// Formatters for one metric sample. Each unit carries its own scale, so a
// label never mixes a compacted number with the raw unit the series is
// stored in: a millisecond series that grew past a million used to read
// "3.80M ms", which is a correct number with an unreadable unit.

export function formatBytes(value: number): string {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(1)} GB`;
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${Math.round(value)} B`;
}

// formatDurationMs climbs the time ladder instead of the decimal one:
// 1500 -> "1.5 s", 90000 -> "1.5 min", 3.8e6 -> "1.1 h".
export function formatDurationMs(value: number): string {
  if (value >= 3_600_000) return `${(value / 3_600_000).toFixed(1)} h`;
  if (value >= 60_000) return `${(value / 60_000).toFixed(1)} min`;
  if (value >= 1000) return `${(value / 1000).toFixed(1)} s`;
  return `${Math.round(value)} ms`;
}

// formatMetricSample renders one value for an axis tick or a tooltip row.
// A unit-less series (counts) stays bare; every other unit is either folded
// into the value (ms, B) or appended behind the compacted number.
export function formatMetricSample(value: number, unit: string): string {
  if (unit === 'B') return formatBytes(value);
  if (unit === 'ms') return formatDurationMs(value);
  return unit === '' ? formatCompact(value) : `${formatCompact(value)} ${unit}`;
}
