// perfMetrics holds the renderer measurements the probe reports but does
// not take itself: the flush-to-frame cost of a streaming commit, how long
// a markdown block took to render, and the wall time of the top-level
// interactions (send, session switch, opening and saving settings). The
// probe owns the report clock; this module owns the rings and the window
// accumulator.
//
// Everything here is a no-op until the probe starts sampling, so a page
// with the switch off pays one boolean check per event and nothing per
// frame. Measurements are in performance.now() milliseconds and are only
// taken while the window is visible: a hidden window's frames are
// suspended by the engine, and the number would describe the suspension
// rather than the renderer.

// SUSPENDED_GAP_MS is where a frame-coupled measurement stops describing
// the renderer: past it the frame did not arrive because the page was
// hidden, occluded or asleep. The frame sampler in perfProbe draws the
// same line between "a slow frame" and "no frame".
export const SUSPENDED_GAP_MS = 1_000;

// The rings keep the last few flush-commit and markdown-render timings, the
// same shape the store keeps for flush durations: the percentiles the probe
// reports describe them, so a review compares distributions between builds.
// Fixed size, no growth, no allocation per measurement beyond one number.
const RING_SIZE = 256;
const commitDurations: number[] = [];
const markdownDurations: number[] = [];

// InteractionName is the label a report carries when it names the slowest
// top-level interaction of its window.
export type InteractionName =
  'send' | 'resume' | 'settings-open' | 'settings-save';

let enabled = false;
// suspendSeq counts the times the window hid while sampling. A measurement
// that spans a hide is dropped: its duration holds the suspension, not the
// work, and a renderer that was not painting cannot explain a slow one.
let suspendSeq = 0;
// The interaction window: only the slowest and the count survive, so a 30s
// span of clicks costs two numbers instead of an array of samples.
let interactionMax = 0;
let interactionName: InteractionName | '' = '';
let interactionCount = 0;

function visible(): boolean {
  return (
    typeof document === 'undefined' || document.visibilityState === 'visible'
  );
}

function onVisibilityChange() {
  if (document.visibilityState !== 'visible') suspendSeq += 1;
}

// setPerfMetricsEnabled is the probe's sampler switch: disabled, the rings
// and the window are cleared so a later report cannot mix in numbers taken
// before the switch was flipped.
export function setPerfMetricsEnabled(on: boolean): void {
  if (typeof document !== 'undefined') {
    if (on && !enabled) {
      document.addEventListener('visibilitychange', onVisibilityChange);
    } else if (!on && enabled) {
      document.removeEventListener('visibilitychange', onVisibilityChange);
    }
  }
  enabled = on;
  commitDurations.length = 0;
  markdownDurations.length = 0;
  suspendSeq = 0;
  interactionMax = 0;
  interactionName = '';
  interactionCount = 0;
}

export function perfMetricsEnabled(): boolean {
  return enabled;
}

function record(ring: number[], ms: number): void {
  ring.push(ms);
  if (ring.length > RING_SIZE) ring.shift();
}

// statsFor summarizes one ring the way the probe reports it: nearest-rank
// percentiles over the last RING_SIZE measurements, plus the worst one.
function statsFor(ring: number[]): { p50: number; p95: number; max: number } {
  if (ring.length === 0) return { p50: 0, p95: 0, max: 0 };
  const sorted = [...ring].sort((a, b) => a - b);
  const pick = (q: number) =>
    sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))];
  return { p50: pick(0.5), p95: pick(0.95), max: sorted[sorted.length - 1] };
}

function scheduleFrame(measure: () => void): void {
  if (typeof requestAnimationFrame === 'function') {
    requestAnimationFrame(measure);
    return;
  }
  // No animation frames (a non-visual environment): take the measurement
  // where it stands rather than dropping it.
  measure();
}

// scheduleFlushCommit measures one stream flush to the frame that follows
// it. The store calls this with the flush's start time, right after the
// queued deltas were routed, so the number covers the flush's own work plus
// everything the frame had to do before it painted — what "the transcript
// stutters while streaming" actually feels like. It is taken on the frame,
// which also means a flush that did not make it into one (a gap past
// SUSPENDED_GAP_MS, or a window that hid in between) is not a rendering
// cost and is dropped.
export function scheduleFlushCommit(startedAt: number): void {
  if (!enabled || !visible()) return;
  const seq = suspendSeq;
  scheduleFrame(() => {
    if (seq !== suspendSeq || !visible()) return;
    const ms = performance.now() - startedAt;
    if (ms <= SUSPENDED_GAP_MS) record(commitDurations, ms);
  });
}

export function flushCommitStats(): {
  p50: number;
  p95: number;
  max: number;
} {
  return statsFor(commitDurations);
}

// beginMarkdownRender and endMarkdownRender bracket one Markdown render:
// the start is taken in the component's body, the end in its layout effect,
// so the number spans react-markdown's parse and the commit of the block —
// the expensive half of a streaming flush. A memoized block that did not
// render again never calls the pair, and a disabled sampler returns 0 so
// the effect has nothing to report.
export function beginMarkdownRender(): number {
  if (!enabled || !visible()) return 0;
  return performance.now();
}

export function endMarkdownRender(startedAt: number): void {
  if (startedAt === 0 || !enabled || !visible()) return;
  const ms = performance.now() - startedAt;
  if (ms <= SUSPENDED_GAP_MS) record(markdownDurations, ms);
}

export function markdownStats(): { p50: number; p95: number; max: number } {
  return statsFor(markdownDurations);
}

// measureInteraction times one top-level interaction from its start to the
// frame that follows it: the action's settle is where its work stops, and
// the frame is when the user could see the result. It returns the action's
// own promise, so a caller awaits the action, not the measurement — and a
// failed interaction is still timed, because the frame that rendered the
// failure was work on the same path.
export function measureInteraction<T>(
  name: InteractionName,
  run: () => Promise<T> | T,
): Promise<T> {
  if (!enabled || !visible()) return Promise.resolve(run());
  const startedAt = performance.now();
  const seq = suspendSeq;
  const settle = () => {
    scheduleFrame(() => {
      if (seq !== suspendSeq || !visible()) return;
      const ms = performance.now() - startedAt;
      interactionCount += 1;
      // The first interaction of a window names it even at 0ms: a name
      // that is there says which interaction was timed, and the number
      // alone cannot say that.
      if (interactionName === '' || ms > interactionMax) {
        interactionMax = ms;
        interactionName = name;
      }
    });
  };
  let result: Promise<T> | T;
  try {
    result = run();
  } catch (err) {
    settle();
    throw err;
  }
  return Promise.resolve(result).then(
    (value) => {
      settle();
      return value;
    },
    (err) => {
      settle();
      throw err;
    },
  );
}

// drainInteractions reports the window's slowest interaction with its name,
// and clears the accumulator: a window that had no interaction has nothing
// to report, so the probe sends no sample for it.
export function drainInteractions(): {
  max: number;
  name: InteractionName | '';
  count: number;
} {
  const out = {
    max: interactionMax,
    name: interactionName,
    count: interactionCount,
  };
  interactionMax = 0;
  interactionName = '';
  interactionCount = 0;
  return out;
}
