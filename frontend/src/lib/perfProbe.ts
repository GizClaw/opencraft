import { api } from './api';
import { streamFlushStats, useStore } from './store';

// perfProbe is the renderer-side observability hook that a long-turn
// investigation needs: it reports what the page holds and how expensive
// stream flushing is, every 30s, through the same diagnostics channel the
// web-vitals reporter uses. Samples land in the user metric store as
// frontend.* series, so the diagnostics charts and SQLite queries can
// show them growing (or not) across a session.
//
// The Diagnostics tab owns the persisted switch; a support session can
// also force it on without touching that setting, with
// localStorage['oc.perfProbe'] = '1' / window.__ocPerfProbe = true. The
// frame sampler is a single subtraction per animation frame.
const REPORT_INTERVAL_MS = 30_000;

let started = false;
let timer = 0;
let frameHandle = 0;
let frames = 0;
let frameMaxMs = 0;
let lastFrameAt = 0;
let longTasks = 0;
let longTaskMaxMs = 0;

function sampleFrames(now: number) {
  if (!started) return;
  if (lastFrameAt !== 0) {
    const gap = now - lastFrameAt;
    frames += 1;
    if (gap > frameMaxMs) frameMaxMs = gap;
  }
  lastFrameAt = now;
  frameHandle = requestAnimationFrame(sampleFrames);
}

// Frame sampling pauses while the page is hidden. A background window
// has its animation frames throttled or stopped by the engine, so the
// gap between two samples measures the throttle rather than the
// renderer: the multi-second and even multi-minute frame_max samples in
// the log are all background windows, and they made the metric useless
// for judging whether streaming got cheaper. Hiding drops the baseline
// (the time spent hidden never lands in a sample) and shows it again on
// resume, so frame_max only ever describes visible rendering.
function onVisibilityChange() {
  if (!started) return;
  if (document.visibilityState === 'visible') {
    if (frameHandle === 0) {
      lastFrameAt = 0;
      frameHandle = requestAnimationFrame(sampleFrames);
    }
    return;
  }
  if (frameHandle !== 0) {
    cancelAnimationFrame(frameHandle);
    frameHandle = 0;
  }
  lastFrameAt = 0;
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
  lastFrameAt = 0;
  frameHandle = requestAnimationFrame(sampleFrames);
  timer = window.setInterval(() => void report(), REPORT_INTERVAL_MS);
  document.addEventListener('visibilitychange', onVisibilityChange);
  onVisibilityChange();
}

// stopPerfProbe ends the sampler. The diagnostics switch turns it off
// without a reload, so the page goes back to paying nothing per frame
// and nothing every 30s.
export function stopPerfProbe(): void {
  if (!started) return;
  started = false;
  document.removeEventListener('visibilitychange', onVisibilityChange);
  window.clearInterval(timer);
  timer = 0;
  cancelAnimationFrame(frameHandle);
  frameHandle = 0;
  frames = 0;
  frameMaxMs = 0;
  lastFrameAt = 0;
  longTasks = 0;
  longTaskMaxMs = 0;
}

// setPerfProbeEnabled applies a switch flip in the page. The host owns
// the persisted value; this only makes the change immediate.
export function setPerfProbeEnabled(enabled: boolean): void {
  if (enabled) {
    startPerfProbe();
    return;
  }
  stopPerfProbe();
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
// should run. The Diagnostics tab owns the persisted switch, so a support
// session survives a restart, and localStorage['oc.perfProbe'] /
// window.__ocPerfProbe force it on without touching the setting (a dev
// build, or a session that has to be sampled before the switch exists).
export function installPerfProbe(): void {
  (
    window as unknown as { __ocPerfProbeReport?: () => Promise<void> }
  ).__ocPerfProbeReport = reportPerfProbeNow;
  if (perfProbeEnabled()) {
    startPerfProbe();
    return;
  }
  void api
    .perfProbe()
    .then((enabled) => {
      if (enabled) startPerfProbe();
    })
    .catch(() => {
      // No diagnostics binding (tests, headless): leave the probe off.
    });
}

// perfProbeRunning reports whether the sampler is live in this page. The
// diagnostics card reads it so the switch shows what the renderer is
// actually doing, not just what the host remembered.
export function perfProbeRunning(): boolean {
  return started;
}
