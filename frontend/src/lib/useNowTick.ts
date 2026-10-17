// useNowTick is the clock a relative label reads: it re-renders the caller
// once per interval while `active`, so "in 5 minutes" counts down on the
// screen instead of freezing at the moment the row was drawn.
//
// It ticks only while asked. The expensive part of a second is the render
// it causes, so callers keep it on the smallest component that shows the
// label — a list does not re-render because a countdown in one row moved.

import { useEffect, useState } from 'react';

export function useNowTick(active: boolean, intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    // A tick's worth of staleness is invisible, but a label left over from
    // the last time this was active is not: re-read the clock on the way
    // in.
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [active, intervalMs]);
  return now;
}
