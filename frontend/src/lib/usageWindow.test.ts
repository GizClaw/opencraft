import { describe, expect, it } from 'vitest';
import {
  alignUsageWindow,
  ceilHourMs,
  ceilLocalDayMs,
  floorHourMs,
  localMidnightMs,
} from './usageWindow';

const HOUR = 3_600_000;

describe('hour alignment', () => {
  it('floors to and ceils from whole UTC hours', () => {
    const t = Date.UTC(2026, 8, 15, 14, 45);
    expect(floorHourMs(t)).toBe(Date.UTC(2026, 8, 15, 14, 0));
    expect(ceilHourMs(t)).toBe(Date.UTC(2026, 8, 15, 15, 0));
    expect(floorHourMs(t) % HOUR).toBe(0);
    expect(ceilHourMs(t) % HOUR).toBe(0);
  });

  it('keeps exact whole hours unchanged', () => {
    const t = Date.UTC(2026, 8, 15, 14, 0);
    expect(floorHourMs(t)).toBe(t);
    expect(ceilHourMs(t)).toBe(t);
  });
});

describe('local day alignment', () => {
  it('floors to local midnight', () => {
    const t = new Date(2026, 8, 15, 9, 30).getTime();
    expect(localMidnightMs(t)).toBe(
      new Date(2026, 8, 15, 0, 0, 0, 0).getTime(),
    );
  });

  it('ceils to the next local midnight, excluding an exact midnight', () => {
    const midDay = new Date(2026, 8, 15, 9, 30).getTime();
    expect(ceilLocalDayMs(midDay)).toBe(
      new Date(2026, 8, 16, 0, 0, 0, 0).getTime(),
    );
    const exactMidnight = new Date(2026, 8, 16, 0, 0, 0, 0).getTime();
    expect(ceilLocalDayMs(exactMidnight)).toBe(exactMidnight);
  });
});

describe('alignUsageWindow', () => {
  it('snaps hour windows outward with an exclusive aligned end', () => {
    const start = Date.UTC(2026, 8, 15, 9, 45);
    const end = Date.UTC(2026, 8, 15, 14, 30);
    expect(alignUsageWindow(start, end, 'hour')).toEqual({
      start: Date.UTC(2026, 8, 15, 9, 0),
      end: Date.UTC(2026, 8, 15, 15, 0),
    });
  });

  it('snaps day windows to local day boundaries', () => {
    const start = new Date(2026, 8, 15, 9, 30).getTime();
    const end = new Date(2026, 8, 17, 18, 20).getTime();
    expect(alignUsageWindow(start, end, 'day')).toEqual({
      start: new Date(2026, 8, 15, 0, 0, 0, 0).getTime(),
      end: new Date(2026, 8, 18, 0, 0, 0, 0).getTime(),
    });
  });
});
