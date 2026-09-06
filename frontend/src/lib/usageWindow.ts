// Usage charts are bucketed into whole UTC hours (hour granularity) or
// local calendar days (day granularity). The backend includes a bucket
// when its start instant lies in [start, end), so a selection whose
// edges fall inside a bucket cannot be shown half-open. Both the
// backend query and the chart's zero-fill therefore snap the window
// outward to bucket boundaries: the bucket containing the start edge
// and the bucket containing the end edge are included, and the end is
// exclusive. Keeping one shared rule here guarantees the chart never
// renders buckets the query excluded (or vice versa).

export const HOUR_MS = 3_600_000;
export const DAY_MS = 86_400_000;
export const MAX_USAGE_RANGE_DAYS = 366;

export function floorHourMs(ms: number): number {
  return Math.floor(ms / HOUR_MS) * HOUR_MS;
}

export function ceilHourMs(ms: number): number {
  return Math.ceil(ms / HOUR_MS) * HOUR_MS;
}

export function localMidnightMs(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

// ceilLocalDayMs returns the start of the local day after the one
// containing ms; when ms is exactly a local midnight it returns ms so
// that day stays excluded, matching the [start, end) bucket rule.
export function ceilLocalDayMs(ms: number): number {
  const dayStart = localMidnightMs(ms);
  if (dayStart === ms) return ms;
  const d = new Date(dayStart);
  d.setDate(d.getDate() + 1);
  return d.getTime();
}

export function alignUsageWindow(
  startMs: number,
  endMs: number,
  granularity: 'hour' | 'day',
): { start: number; end: number } {
  if (granularity === 'hour') {
    return { start: floorHourMs(startMs), end: ceilHourMs(endMs) };
  }
  return { start: localMidnightMs(startMs), end: ceilLocalDayMs(endMs) };
}
