import { useEffect, useState } from 'react';
import type { ToolView } from './store';

// workedForLabel renders a duration as "1h 2m 3s" (or "<1s" for
// sub-second spans). It is the shared formatter for every duration the
// UI shows: the backend-computed turn duration (turn_end's duration_ms,
// replayed from the archive for resumed turns) and the per-call spans
// this client measures itself.
export function workedForLabel(durationMs?: number): string {
  if (
    durationMs === undefined ||
    !Number.isFinite(durationMs) ||
    durationMs < 0
  ) {
    return '';
  }
  if (durationMs < 1000) return '<1s';
  const total = Math.floor(durationMs / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const parts = [];
  if (h > 0) parts.push(`${h}h`);
  if (m > 0 || h > 0) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(' ');
}

// toolDurationMs reports how long one call has been taking: while it
// runs, the time since this client saw the call; once it finishes, the
// frozen span between the call and its result. It returns undefined
// when the client never saw the call start — turns reloaded from the
// archive carry no per-call timing, and a card with no duration beats
// one with an invented number.
export function toolDurationMs(
  tool: Pick<ToolView, 'status' | 'seenAt' | 'endedAt'>,
  now: number = Date.now(),
): number | undefined {
  const { seenAt, endedAt } = tool;
  if (seenAt === undefined) return undefined;
  if (tool.status === 'running') return Math.max(0, now - seenAt);
  if (endedAt === undefined) return undefined;
  return Math.max(0, endedAt - seenAt);
}

// minVisibleMs hides sub-second spans: a one-line status that flashes
// "0s" is noise, and the same threshold keeps the live reading and the
// frozen one consistent.
const minVisibleMs = 1000;

// useToolElapsedLabel renders one call's duration: it ticks once a
// second while the call runs and stops at the frozen value once the
// result lands. Short calls render nothing.
export function useToolElapsedLabel(tool: ToolView | null): string {
  const running = tool?.status === 'running';
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [running]);
  const ms = tool ? toolDurationMs(tool, now) : undefined;
  if (ms === undefined || ms < minVisibleMs) return '';
  return workedForLabel(ms);
}
