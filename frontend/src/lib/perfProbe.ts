import { System } from '@wailsio/runtime';
import { api } from './api';
import { streamFlushStats, useStore } from './store';

// perfProbe is the renderer-side observability hook that a long-turn
// investigation needs: it reports what the page holds and how expensive
// stream flushing is, every 30s, through the same diagnostics channel the
// web-vitals reporter uses. Samples land in the user metric store as
// frontend.* series, so the diagnostics charts and SQLite queries can
// show them growing (or not) across a session.
//
// It is enabled when the host reports a debug build, or explicitly with
// localStorage['oc.perfProbe'] = '1' / window.__ocPerfProbe = true. The
// frame sampler is a single subtraction per animation frame.
const REPORT_INTERVAL_MS = 30_000;

let started = false;
let frames = 0;
let frameMaxMs = 0;
let lastFrameAt = 0;
let longTasks = 0;
let longTaskMaxMs = 0;

function sampleFrames(now: number) {
  if (lastFrameAt !== 0) {
    const gap = now - lastFrameAt;
    frames += 1;
    if (gap > frameMaxMs) frameMaxMs = gap;
  }
  lastFrameAt = now;
  requestAnimationFrame(sampleFrames);
}

// countMessages sums the loaded transcript rows across conversations;
// it is the cheap proxy for "how much does the renderer hold".
function countMessages(): number {
  const state = useStore.getState();
  return Object.values(state.conversations).reduce(
    (sum, conv) => sum + conv.messages.length,
    0,
  );
}

async function report() {
  const flush = streamFlushStats();
  const samples: Array<{ name: string; value: number; unit: string }> = [
    {
      name: 'dom_nodes',
      value: document.getElementsByTagName('*').length,
      unit: '1',
    },
    { name: 'frame_max', value: frameMaxMs, unit: 'ms' },
    { name: 'frames', value: frames, unit: '1' },
    { name: 'flush_p50', value: flush.p50, unit: 'ms' },
    { name: 'flush_p95', value: flush.p95, unit: 'ms' },
    { name: 'flush_max', value: flush.max, unit: 'ms' },
    { name: 'flush_count', value: flush.count, unit: '1' },
    { name: 'conv_messages', value: countMessages(), unit: '1' },
  ];
  if (longTasks > 0) {
    samples.push({ name: 'long_task_max', value: longTaskMaxMs, unit: 'ms' });
    samples.push({ name: 'long_tasks', value: longTasks, unit: '1' });
  }
  frames = 0;
  frameMaxMs = 0;
  longTasks = 0;
  longTaskMaxMs = 0;
  try {
    await api.reportFrontendPerf(samples);
  } catch {
    // Diagnostics is best-effort: a dead binding must not affect the UI.
  }
}

// reportNow lets a developer capture a sample immediately from the
// console: `__ocPerfProbeReport()`.
export function reportPerfProbeNow(): Promise<void> {
  return report();
}

export function startPerfProbe(): void {
  if (started) return;
  started = true;
  try {
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        longTasks += 1;
        if (entry.duration > longTaskMaxMs) longTaskMaxMs = entry.duration;
      }
    }).observe({ type: 'longtask', buffered: false });
  } catch {
    // longtask is Chromium-only; frame gaps cover the other engines.
  }
  requestAnimationFrame(sampleFrames);
  window.setInterval(() => void report(), REPORT_INTERVAL_MS);
}

export function perfProbeEnabled(): boolean {
  try {
    if (localStorage.getItem('oc.perfProbe') === '1') return true;
  } catch {
    // localStorage can be unavailable; fall through to the global flag.
  }
  return (
    (window as unknown as { __ocPerfProbe?: boolean }).__ocPerfProbe === true
  );
}

// installPerfProbe decides from the host environment whether the probe
// should run. Debug builds (the local packaged app) get it by default so
// a long-turn report is available without rebuilding; release builds stay
// quiet unless the user opts in.
export function installPerfProbe(): void {
  (
    window as unknown as { __ocPerfProbeReport?: () => Promise<void> }
  ).__ocPerfProbeReport = reportPerfProbeNow;
  if (perfProbeEnabled()) {
    startPerfProbe();
    return;
  }
  void System.Environment()
    .then((env) => {
      if (env.Debug) startPerfProbe();
    })
    .catch(() => {
      // No environment binding (tests, headless): leave the probe off.
    });
}
