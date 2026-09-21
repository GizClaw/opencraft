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
});
