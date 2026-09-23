import { api } from './api';
import {
  drainInteractions,
  flushCommitStats,
  markdownStats,
  setPerfMetricsEnabled,
  SUSPENDED_GAP_MS,
} from './perfMetrics';
import { buildVersion, reportLabels } from './perfLabels';
import { streamFlushStats, useStore } from './store';

// perfProbe is the renderer-side observability hook that a long-turn
// investigation needs: it reports what the page holds and how expensive
// stream flushing is, every 30s the window is visible, through the same
// diagnostics channel the web-vitals reporter uses. Samples land in the
// user metric store as
// frontend.* series, so the diagnostics charts and SQLite queries can
// show them growing (or not) across a session. Every report is labeled
// with the surface, the view in front, the app build and the active
// conversation id, so a series stays attributable after the fact.
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
let longFrames50 = 0;
let longFrames100 = 0;
let longFrames200 = 0;
let droppedGaps = 0;
// windowStartedAt is when the span the next report covers began. A window
// is normally the 30s report interval, but a report is skipped while the
// page is hidden, so the sample after a hidden stretch covers all of it:
// window_ms is what keeps frames and the flush counts comparable across
// windows after that.
let windowStartedAt = 0;
// reportedFlushTotal is the store's page-lifetime flush count at the last
// report: a report carries the flushes of its own window (total now minus
// total then), never the store's ring size.
let reportedFlushTotal = 0;

// The three frame budgets a gap has to blow to land in the matching
// long_frames_* counter: a frame the user can feel, one that visibly
// stutters, and one that reads as a stall. Counts rather than only the
// window's worst frame, because a p99 frame_max cannot say whether a slow
// window held one bad frame or fifty. The Chromium-only 'longtask' entry
// type this replaced never fired in the shell's engine (WebKit on macOS
// and Linux), so its series stayed silently empty; a frame gap is sampled
// on every engine.
const LONG_FRAME_MS = 50;
const SEVERE_FRAME_MS = 100;
const STALL_FRAME_MS = 200;

// SUSPENDED_GAP_MS (perfMetrics) is where a gap stops being a frame at
// all. Animation frames stop for reasons the page cannot see: WebKit
// throttles a window another window occludes without firing
// visibilitychange, a native menu runs the app's own run loop, and a
// sleeping machine draws nothing. The log's 272-second and 4.5-minute
// frame_max samples were all of this, and folding them into the metric is
// what made it useless for judging whether rendering got cheaper. A longer
// gap is dropped from frames, frame_max and long_frames_* and counted in
// dropped_gaps instead: a window that was suspended — or stalled so long
// that nothing could have painted — shows up as one that was not sampled,
// rather than as a quiet one.

function sampleFrames(now: number) {
  if (!started) return;
  if (lastFrameAt !== 0) {
    const gap = now - lastFrameAt;
    if (gap <= SUSPENDED_GAP_MS) {
      frames += 1;
      if (gap > frameMaxMs) frameMaxMs = gap;
      if (gap >= LONG_FRAME_MS) longFrames50 += 1;
      if (gap >= SEVERE_FRAME_MS) longFrames100 += 1;
      if (gap >= STALL_FRAME_MS) longFrames200 += 1;
    } else {
      droppedGaps += 1;
    }
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

// mountedRows counts the message rows the active transcript has mounted in
// the DOM ([data-msg-index] rows, see ChatView); it is the DOM-side
// counterpart of countMessages' store-side count.
function mountedRows(): number {
  return document.querySelectorAll('[data-msg-index]').length;
}

async function report() {
  const reportedAt = Date.now();
  const windowMs = windowStartedAt === 0 ? 0 : reportedAt - windowStartedAt;
  windowStartedAt = reportedAt;
  const build = await buildVersion();
  const flush = streamFlushStats();
  const flushCount = flush.total - reportedFlushTotal;
  reportedFlushTotal = flush.total;
  const commit = flushCommitStats();
  const markdown = markdownStats();
  const labels = reportLabels(build);
  const samples: Array<{
    name: string;
    value: number;
    unit: string;
    labels: Record<string, string>;
  }> = [
    { name: 'window_ms', value: windowMs, unit: 'ms', labels },
    {
      name: 'dom_nodes',
      value: document.getElementsByTagName('*').length,
      unit: '1',
      labels,
    },
    { name: 'frame_max', value: frameMaxMs, unit: 'ms', labels },
    { name: 'frames', value: frames, unit: '1', labels },
    { name: 'dropped_gaps', value: droppedGaps, unit: '1', labels },
    { name: 'long_frames_50', value: longFrames50, unit: '1', labels },
    { name: 'long_frames_100', value: longFrames100, unit: '1', labels },
    { name: 'long_frames_200', value: longFrames200, unit: '1', labels },
    { name: 'flush_p50', value: flush.p50, unit: 'ms', labels },
    { name: 'flush_p95', value: flush.p95, unit: 'ms', labels },
    { name: 'flush_max', value: flush.max, unit: 'ms', labels },
    { name: 'flush_count', value: flushCount, unit: '1', labels },
    // The flush's other half: how long the frame it landed in took, and
    // what one markdown block cost to render (see perfMetrics). Unlike
    // frames these are not window sums — they are the ring's percentiles,
    // reported every window so a build's baseline can be compared.
    { name: 'flush_commit_p50', value: commit.p50, unit: 'ms', labels },
    { name: 'flush_commit_p95', value: commit.p95, unit: 'ms', labels },
    { name: 'flush_commit_max', value: commit.max, unit: 'ms', labels },
    { name: 'flush_md_p95', value: markdown.p95, unit: 'ms', labels },
    { name: 'conv_messages', value: countMessages(), unit: '1', labels },
    { name: 'mounted_rows', value: mountedRows(), unit: '1', labels },
  ];
  // Only a window that held an interaction carries the sample, and the
  // slowest one names it in the label: `interaction_max` alone could not
  // say whether the stall was a send, a session switch, or a settings
  // save. Streaming flushes are deliberately not counted here — they have
  // their own series, and a measurement taken on every frame would drown
  // the four interactions this exists to attribute.
  const interaction = drainInteractions();
  if (interaction.count > 0) {
    samples.push({
      name: 'interaction_max',
      value: interaction.max,
      unit: 'ms',
      labels: { ...labels, interaction: interaction.name },
    });
  }
  frames = 0;
  frameMaxMs = 0;
  longFrames50 = 0;
  longFrames100 = 0;
  longFrames200 = 0;
  droppedGaps = 0;
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
  // The measurements taken outside the sampler (flush commits, markdown
  // renders, interactions) start paying attention too, and their rings
  // start empty so the first report holds only this session's numbers.
  setPerfMetricsEnabled(true);
  // The first window starts here: flushes that happened before the sampler
  // was on belong to no report.
  reportedFlushTotal = streamFlushStats().total;
  windowStartedAt = Date.now();
  lastFrameAt = 0;
  frameHandle = requestAnimationFrame(sampleFrames);
  timer = window.setInterval(() => {
    // A hidden window draws nothing: its frame counters describe a sampler
    // that was paused, and its gauges a page nobody is looking at. The
    // window itself keeps running — flushes and frame counts resume where
    // they stopped — and the next visible report covers all of it, which
    // window_ms then says out loud.
    if (document.visibilityState !== 'visible') return;
    void report();
  }, REPORT_INTERVAL_MS);
  document.addEventListener('visibilitychange', onVisibilityChange);
  onVisibilityChange();
}

// stopPerfProbe ends the sampler. The diagnostics switch turns it off
// without a reload, so the page goes back to paying nothing per frame
// and nothing every 30s.
export function stopPerfProbe(): void {
  if (!started) return;
  started = false;
  setPerfMetricsEnabled(false);
  document.removeEventListener('visibilitychange', onVisibilityChange);
  window.clearInterval(timer);
  timer = 0;
  cancelAnimationFrame(frameHandle);
  frameHandle = 0;
  frames = 0;
  frameMaxMs = 0;
  lastFrameAt = 0;
  longFrames50 = 0;
  longFrames100 = 0;
  longFrames200 = 0;
  droppedGaps = 0;
  windowStartedAt = 0;
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
