// A live end ("now") has to keep saying now while the panel is open; the
// field used to hold the timestamp from the moment the panel was opened.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { UsageRangePicker } from './UsageRangePicker';

afterEach(() => {
  vi.useRealTimers();
});

describe('UsageRangePicker', () => {
  it('keeps a live end on the clock while the panel is open', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-21T09:00:00Z'));
    try {
      render(
        <UsageRangePicker
          active
          startMs={Date.parse('2026-09-20T09:00:00Z')}
          endMs={Date.parse('2026-09-21T09:00:00Z')}
          liveEnd
          onApply={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByRole('button'));

      const end = screen.getByLabelText('End time') as HTMLInputElement;
      // The field is a local datetime, so the assertion goes through the
      // same parse the browser would: one tick is one minute later.
      const openedAt = new Date(end.value).getTime();
      expect(openedAt).toBe(Date.parse('2026-09-21T09:00:00Z'));

      act(() => {
        vi.advanceTimersByTime(60_000);
      });
      expect(new Date(end.value).getTime() - openedAt).toBe(60_000);
    } finally {
      vi.useRealTimers();
    }
  });
});
