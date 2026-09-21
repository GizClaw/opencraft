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
}));
const storeMock = vi.hoisted(() => ({
  useStore: { getState: () => ({ conversations: {} }) },
  streamFlushStats: () => ({ p50: 0, p95: 0, max: 0, count: 0 }),
}));

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

beforeAll(() => {
  // jsdom only runs animation frames in visual mode; the sampler's frame
  // walk is not what these tests are about.
  vi.stubGlobal('requestAnimationFrame', () => 0);
  vi.stubGlobal('cancelAnimationFrame', () => {});
});

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
});

afterEach(() => {
  stopPerfProbe();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

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
      expect.arrayContaining(['dom_nodes', 'conv_messages', 'flush_p95']),
    );

    stopPerfProbe();
    expect(perfProbeRunning()).toBe(false);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(apiMock.reportFrontendPerf).toHaveBeenCalledTimes(1);
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
    const scheduled = new Map<number, (now: number) => void>();
    let nextHandle = 1;
    vi.stubGlobal('requestAnimationFrame', (cb: (now: number) => void) => {
      const handle = nextHandle++;
      scheduled.set(handle, cb);
      return handle;
    });
    const cancelled: number[] = [];
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

    const samples = async () => {
      await reportPerfProbeNow();
      const last = apiMock.reportFrontendPerf.mock.calls.at(-1);
      const rows = last?.[0] as Array<{ name: string; value: number }>;
      return new Map(rows.map((row) => [row.name, row.value]));
    };

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

      const reported = await samples();
      expect(reported.get('frames')).toBe(2);
      expect(reported.get('frame_max')).toBe(16);
    } finally {
      delete (document as unknown as { visibilityState?: string })
        .visibilityState;
    }
  });
});
