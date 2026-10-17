// The tick exists so a relative label keeps moving; these cases pin that
// it runs only while asked and that it starts from the current clock.
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useNowTick } from './useNowTick';

afterEach(() => {
  vi.useRealTimers();
});

describe('useNowTick', () => {
  it('advances once per interval while active', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-16T09:00:00Z'));
    const { result } = renderHook(() => useNowTick(true));

    expect(result.current).toBe(Date.parse('2026-10-16T09:00:00Z'));
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(result.current).toBe(Date.parse('2026-10-16T09:00:03Z'));
  });

  it('does not tick while inactive', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-16T09:00:00Z'));
    const { result } = renderHook(() => useNowTick(false));

    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(result.current).toBe(Date.parse('2026-10-16T09:00:00Z'));
  });

  it('re-reads the clock when it becomes active again', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-16T09:00:00Z'));
    const { result, rerender } = renderHook(
      ({ active }) => useNowTick(active),
      { initialProps: { active: false } },
    );

    // Time passes while nothing is on screen; the label must not show the
    // reading from before.
    act(() => {
      vi.setSystemTime(new Date('2026-10-16T09:05:00Z'));
      rerender({ active: true });
    });
    expect(result.current).toBe(Date.parse('2026-10-16T09:05:00Z'));

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current).toBe(Date.parse('2026-10-16T09:05:01Z'));
  });
});
