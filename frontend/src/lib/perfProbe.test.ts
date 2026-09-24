import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest';

const apiMock = vi.hoisted(() => ({
  perfProbe: vi.fn(),
  reportFrontendPerf: vi.fn(),
  version: vi.fn(async () => '9.9.9-test'),
}));
const storeMock = vi.hoisted(() => {
  // The state the labels are read from: a workbench with a workspace open
  // and no overlay. A test flips one field to pin the route it exercises.
  const state = {
    conversations: {} as Record<string, { messages: unknown[] }>,
    workspace: 'w-1',
    configOpen: false,
    toolsView: null as string | null,
  };
  return {
    state,
    useStore: { getState: () => state },
    streamFlushStats: vi.fn(() => ({ total: 0, p50: 0, p95: 0, max: 0 })),
    storeBytes: vi.fn(() => ({ media: 0, text: 0, conversations: 0 })),
    activeConversationID: vi.fn(() => 'c-42'),
  };
});

vi.mock('./api', () => ({ api: apiMock }));
vi.mock('./store', () => storeMock);

import {
  installPerfProbe,
  perfProbeRunning,
  reportPerfProbeNow,
  setPerfProbeEnabled,
  startPerfProbe,
  stopPerfProbe,
} from './perfProbe';
import { measureInteraction } from './perfMetrics';

beforeAll(() => {
  // jsdom only runs animation frames in visual mode; the sampler's frame
  // walk is not what these tests are about.
  vi.stubGlobal('requestAnimationFrame', () => 0);
  vi.stubGlobal('cancelAnimationFrame', () => {});
});

beforeEach(() => {
  vi.clearAllMocks();
  storeMock.streamFlushStats.mockReturnValue({
    total: 0,
    p50: 0,
    p95: 0,
    max: 0,
  });
  storeMock.storeBytes.mockReturnValue({
    media: 0,
    text: 0,
    conversations: 0,
  });
  storeMock.state.workspace = 'w-1';
  storeMock.state.configOpen = false;
  storeMock.state.toolsView = null;
  window.localStorage.clear();
});

afterEach(() => {
  stopPerfProbe();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// frameHarness replaces the sampler's animation frame with a queue a test
// fires by hand, with the timestamps it wants the sampler to see: jsdom
// does not run animation frames, and what a gap means is the whole point
// of these tests.
function frameHarness() {
  const scheduled = new Map<number, (now: number) => void>();
  const cancelled: number[] = [];
  let nextHandle = 1;
  vi.stubGlobal('requestAnimationFrame', (cb: (now: number) => void) => {
    const handle = nextHandle++;
    scheduled.set(handle, cb);
    return handle;
  });
  vi.stubGlobal('cancelAnimationFrame', (handle: number) => {
    cancelled.push(handle);
    scheduled.delete(handle);
  });
  const fireFrame = (now: number) => {
    const next = [...scheduled.entries()].at(-1);
    if (!next) throw new Error('no animation frame scheduled');
    scheduled.delete(next[0]);
    next[1](now);
  };
  return { scheduled, cancelled, fireFrame };
}

// lastSampleValues reports now and reads that report's samples as a map,
// which is how every assertion here reads a window.
async function lastSampleValues(): Promise<Map<string, number>> {
  await reportPerfProbeNow();
  const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
  return new Map(
    (samples as Array<{ name: string; value: number }>).map((s) => [
      s.name,
      s.value,
    ]),
  );
}

describe('perfProbe', () => {
  it('starts once, reports on the interval, and stops cleanly', async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(window, 'setInterval');

    startPerfProbe();
    startPerfProbe();
    expect(perfProbeRunning()).toBe(true);
    expect(setIntervalSpy).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(30_000);
    expect(apiMock.reportFrontendPerf).toHaveBeenCalledTimes(1);
    const samples = apiMock.reportFrontendPerf.mock.calls[0][0] as Array<{
      name: string;
    }>;
    expect(samples.map((s) => s.name)).toEqual(
      expect.arrayContaining([
        'dom_nodes',
        'conv_messages',
        'mounted_rows',
        'store_media_bytes',
        'store_text_bytes',
        'loaded_convs',
        'dom_images',
        'flush_p95',
        'flush_commit_p95',
        'flush_md_p95',
      ]),
    );
    // No interaction happened in this window, so nothing claims one did.
    expect(samples.map((s) => s.name)).not.toContain('interaction_max');

    stopPerfProbe();
    expect(perfProbeRunning()).toBe(false);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(apiMock.reportFrontendPerf).toHaveBeenCalledTimes(1);
  });

  it('reports what the loaded transcripts hold', async () => {
    // These three series are what a memory review reads beside
    // proc.mem.footprint: the store's own account of the bytes it keeps,
    // and how many conversations are loaded at all.
    storeMock.storeBytes.mockReturnValue({
      media: 21 << 20,
      text: 3 << 20,
      conversations: 2,
    });
    const values = await lastSampleValues();
    expect(values.get('store_media_bytes')).toBe(21 << 20);
    expect(values.get('store_text_bytes')).toBe(3 << 20);
    expect(values.get('loaded_convs')).toBe(2);
  });

  it('labels every sample with surface, view, build and the active conversation', async () => {
    vi.useFakeTimers();
    startPerfProbe();
    await vi.advanceTimersByTimeAsync(30_000);
    const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
    const rows = samples as Array<{ labels?: Record<string, string> }>;
    expect(rows.length).toBeGreaterThan(0);
    for (const row of rows) {
      expect(row.labels).toEqual({
        surface: 'main',
        route: 'chat',
        build: '9.9.9-test',
        conversation_id: 'c-42',
      });
    }
  });

  it('names the slowest interaction of the window in its label', async () => {
    vi.useFakeTimers();
    const { fireFrame } = frameHarness();
    startPerfProbe();

    const interaction = measureInteraction('settings-open', () => {});
    await interaction;
    // The measurement stops on the frame that follows the interaction; the
    // frame the sampler keeps asking for is not the one being fired.
    fireFrame(0);

    await reportPerfProbeNow();
    const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
    const row = (
      samples as Array<{
        name: string;
        value: number;
        labels?: Record<string, string>;
      }>
    ).find((sample) => sample.name === 'interaction_max');
    expect(row?.labels?.interaction).toBe('settings-open');
    expect(row?.value).toBeGreaterThanOrEqual(0);
  });

  it('names the view in front as the route', async () => {
    // The surface alone could not attribute a long frame: the workbench
    // is the same surface whether the user was scrolling a transcript or
    // sitting in a settings page.
    const route = async () => {
      await reportPerfProbeNow();
      const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
      const rows = samples as Array<{ labels?: Record<string, string> }>;
      return rows[0].labels?.route;
    };

    expect(await route()).toBe('chat');
    storeMock.state.configOpen = true;
    expect(await route()).toBe('settings');
    storeMock.state.configOpen = false;
    storeMock.state.toolsView = 'agents';
    expect(await route()).toBe('tools:agents');
    storeMock.state.toolsView = null;
    storeMock.state.workspace = '';
    expect(await route()).toBe('welcome');
  });

  it('omits labels whose value is not there yet', async () => {
    storeMock.activeConversationID.mockReturnValueOnce('');
    await reportPerfProbeNow();
    const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
    const rows = samples as Array<{ labels?: Record<string, string> }>;
    expect(rows[0].labels).toEqual({
      surface: 'main',
      route: 'chat',
      build: '9.9.9-test',
    });
  });

  it('counts the transcript rows mounted in the DOM', async () => {
    const rows = [0, 1, 2].map((i) => {
      const row = document.createElement('div');
      row.setAttribute('data-msg-index', String(i));
      document.body.appendChild(row);
      return row;
    });
    try {
      await reportPerfProbeNow();
      const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
      const values = new Map(
        (samples as Array<{ name: string; value: number }>).map((s) => [
          s.name,
          s.value,
        ]),
      );
      expect(values.get('mounted_rows')).toBe(3);
    } finally {
      for (const row of rows) row.remove();
    }
  });

  it('applies a diagnostics switch flip immediately', () => {
    setPerfProbeEnabled(true);
    expect(perfProbeRunning()).toBe(true);
    setPerfProbeEnabled(false);
    expect(perfProbeRunning()).toBe(false);
  });

  it('follows the host switch at startup', async () => {
    apiMock.perfProbe.mockResolvedValueOnce(false);
    installPerfProbe();
    await vi.waitFor(() => expect(apiMock.perfProbe).toHaveBeenCalled());
    expect(perfProbeRunning()).toBe(false);

    apiMock.perfProbe.mockResolvedValueOnce(true);
    installPerfProbe();
    await vi.waitFor(() => expect(perfProbeRunning()).toBe(true));
  });

  it('lets the window flag force the sampler on without the setting', async () => {
    // The localStorage override is the other half of this path; jsdom's
    // storage is not the object the module reads under Node's global
    // localStorage, so the window flag is what this test can pin.
    (window as unknown as { __ocPerfProbe?: boolean }).__ocPerfProbe = true;
    installPerfProbe();
    expect(perfProbeRunning()).toBe(true);
    expect(apiMock.perfProbe).not.toHaveBeenCalled();
    delete (window as unknown as { __ocPerfProbe?: boolean }).__ocPerfProbe;
  });

  it('stays off when the diagnostics binding is missing', async () => {
    apiMock.perfProbe.mockRejectedValueOnce(new Error('no binding'));
    installPerfProbe();
    await vi.waitFor(() => expect(apiMock.perfProbe).toHaveBeenCalled());
    expect(perfProbeRunning()).toBe(false);
  });

  it('does not count time spent hidden as a dropped frame', async () => {
    // A background window throttles animation frames, so the gap between
    // two samples measures the throttle, not the renderer: the log's
    // multi-minute frame_max samples were all hidden windows. The sampler
    // has to pause with the page and drop its baseline.
    let visibility: 'visible' | 'hidden' = 'visible';
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => visibility,
    });
    const { scheduled, cancelled, fireFrame } = frameHarness();

    try {
      startPerfProbe();
      // One visible frame pair, 16ms apart.
      fireFrame(1_000);
      fireFrame(1_016);

      visibility = 'hidden';
      document.dispatchEvent(new Event('visibilitychange'));
      expect(cancelled.length).toBeGreaterThan(0);
      expect(scheduled.size).toBe(0);

      // The window comes back a minute later: the first frame after the
      // resume starts a fresh baseline instead of reporting the gap.
      visibility = 'visible';
      document.dispatchEvent(new Event('visibilitychange'));
      expect(scheduled.size).toBe(1);
      fireFrame(60_000);
      fireFrame(60_016);

      const reported = await lastSampleValues();
      expect(reported.get('frames')).toBe(2);
      expect(reported.get('frame_max')).toBe(16);
    } finally {
      delete (document as unknown as { visibilityState?: string })
        .visibilityState;
    }
  });

  it('counts long frames into the budget buckets, on any engine', async () => {
    // longtask is Chromium-only, so long_task_max/long_tasks stayed empty
    // under WebKit; the frame sampler sees every gap on every engine. The
    // counts, not just the window's worst frame, are what say whether a
    // slow window held one bad frame or fifty.
    const { fireFrame } = frameHarness();
    startPerfProbe();
    fireFrame(1_000);
    fireFrame(1_016); // on budget
    fireFrame(1_076); // 60ms: felt
    fireFrame(1_196); // 120ms: a visible stutter
    fireFrame(1_446); // 250ms: a stall
    const values = await lastSampleValues();
    expect(values.get('frames')).toBe(4);
    expect(values.get('frame_max')).toBe(250);
    expect(values.get('long_frames_50')).toBe(3);
    expect(values.get('long_frames_100')).toBe(2);
    expect(values.get('long_frames_200')).toBe(1);
    expect(values.get('dropped_gaps')).toBe(0);
  });

  it('drops a gap the sampler slept through instead of calling it a frame', async () => {
    // An occluded window and a sleeping machine both stop animation
    // frames without telling the page, and the gap that follows measures
    // the suspension: the 272-second frame_max samples in the log were
    // all of this. A gap past a second is not a frame, so it is counted
    // as one that was not sampled rather than folded into frame_max.
    const { fireFrame } = frameHarness();
    startPerfProbe();
    fireFrame(1_000);
    fireFrame(1_016); // on budget
    fireFrame(121_016); // two minutes later: a suspension, not a frame
    fireFrame(121_032);
    const values = await lastSampleValues();
    expect(values.get('frames')).toBe(2);
    expect(values.get('frame_max')).toBe(16);
    expect(values.get('long_frames_50')).toBe(0);
    expect(values.get('dropped_gaps')).toBe(1);
  });

  it('skips a report from a hidden window and covers the time in the next', async () => {
    // A hidden window draws nothing: its frame counters describe a paused
    // sampler and its gauges a page nobody is looking at — the log's
    // 12k-node rows with zero frames were exactly that. Waiting keeps the
    // series readable, and window_ms is what keeps the sample that
    // follows comparable to a 30s one.
    vi.useFakeTimers();
    let visibility: 'visible' | 'hidden' = 'hidden';
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => visibility,
    });
    try {
      startPerfProbe();
      await vi.advanceTimersByTimeAsync(30_000);
      expect(apiMock.reportFrontendPerf).not.toHaveBeenCalled();

      visibility = 'visible';
      document.dispatchEvent(new Event('visibilitychange'));
      await vi.advanceTimersByTimeAsync(30_000);
      expect(apiMock.reportFrontendPerf).toHaveBeenCalledTimes(1);
      const [samples] = apiMock.reportFrontendPerf.mock.calls[0];
      const values = new Map(
        (samples as Array<{ name: string; value: number }>).map((s) => [
          s.name,
          s.value,
        ]),
      );
      expect(values.get('window_ms')).toBe(60_000);
    } finally {
      delete (document as unknown as { visibilityState?: string })
        .visibilityState;
    }
  });

  it('reports the flushes of each window, not the sample ring size', async () => {
    const flushSamples = async (total: number) => {
      storeMock.streamFlushStats.mockReturnValue({
        total,
        p50: 1,
        p95: 2,
        max: 3,
      });
      await reportPerfProbeNow();
      const [samples] = apiMock.reportFrontendPerf.mock.calls.at(-1)!;
      return new Map(
        (samples as Array<{ name: string; value: number }>).map((s) => [
          s.name,
          s.value,
        ]),
      );
    };
    storeMock.streamFlushStats.mockReturnValue({
      total: 40,
      p50: 1,
      p95: 2,
      max: 3,
    });
    startPerfProbe(); // baselines the page-lifetime total
    // The first report diffs against that baseline...
    expect((await flushSamples(47)).get('flush_count')).toBe(7);
    // ...and a window without flushes reports zero, not the ring size.
    expect((await flushSamples(47)).get('flush_count')).toBe(0);
    expect((await flushSamples(47)).get('flush_p95')).toBe(2);
  });
});
