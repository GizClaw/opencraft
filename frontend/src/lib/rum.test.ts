import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  version: vi.fn(async () => '9.9.9-test'),
}));
const storeMock = vi.hoisted(() => ({
  activeConversationID: vi.fn(() => 'c-42'),
  useStore: {
    getState: () => ({
      conversations: {},
      workspace: 'w-1',
      configOpen: false,
      toolsView: null,
    }),
  },
}));
const bindingMock = vi.hoisted(() => ({ ReportFrontendPerf: vi.fn() }));
const vitalsMock = vi.hoisted(() => ({
  inp: null as null | ((metric: unknown) => void),
}));
// The switch that gates this channel lives in the probe; the tests flip
// it directly instead of starting a real sampler.
const probeMock = vi.hoisted(() => ({ running: false }));

vi.mock('./api', () => ({ api: apiMock }));
vi.mock('./store', () => storeMock);
vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => bindingMock,
);
vi.mock('web-vitals', () => ({
  onFID: () => {},
  onINP: (cb: (metric: unknown) => void) => {
    vitalsMock.inp = cb;
  },
}));
vi.mock('./perfProbe', () => ({
  perfProbeRunning: () => probeMock.running,
}));

import { navigationSamples, startRUM } from './rum';

function nav(
  fields: Partial<PerformanceNavigationTiming>,
): PerformanceNavigationTiming {
  return {
    startTime: 0,
    domContentLoadedEventEnd: 0,
    loadEventEnd: 0,
    responseStart: 0,
    ...fields,
  } as PerformanceNavigationTiming;
}

// The first pass runs while the page is still loading: the entry exists but
// its event fields are zero. Sending those zeros is what kept
// frontend.dom_content_loaded and frontend.load off the charts (a 0ms load
// is also a lie), so they are skipped until the event has actually fired.
describe('navigationSamples', () => {
  it('skips the events that have not happened yet', () => {
    expect(navigationSamples(nav({}))).toEqual([]);
    expect(
      navigationSamples(
        nav({ domContentLoadedEventEnd: 0, loadEventEnd: 512 }),
      ),
    ).toEqual([{ name: 'load', value: 512, unit: 'ms' }]);
  });

  it('reports each event as an offset from the navigation start', () => {
    expect(
      navigationSamples(
        nav({
          startTime: 12,
          domContentLoadedEventEnd: 312,
          loadEventEnd: 812,
        }),
      ),
    ).toEqual([
      { name: 'dom_content_loaded', value: 300, unit: 'ms' },
      { name: 'load', value: 800, unit: 'ms' },
    ]);
  });

  // F-37: web vitals and the perf probe write through the same
  // ReportFrontendPerf binding, so the "frontend perf sampling" switch
  // has to gate both — a probe-only kill switch still shipped
  // dom_content_loaded/load/fid/inp off the machine.
  it('sends nothing while the sampling switch is off', async () => {
    probeMock.running = false;
    startRUM();
    expect(vitalsMock.inp).toBeTypeOf('function');

    vitalsMock.inp!({ name: 'INP', id: 'v2', value: 240 });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(bindingMock.ReportFrontendPerf).not.toHaveBeenCalled();
  });
});

// startRUM wires the vitals once per page; the samples it sends carry the
// same labels the probe stamps, so a web-vital stays attributable to the
// shell and build that produced it.
describe('startRUM labels', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.version.mockImplementation(async () => '9.9.9-test');
    storeMock.activeConversationID.mockReturnValue('c-42');
    probeMock.running = true;
  });

  it('stamps a web-vital sample with surface, view, build and conversation', async () => {
    startRUM();
    expect(vitalsMock.inp).toBeTypeOf('function');
    vitalsMock.inp!({ name: 'INP', id: 'v1', value: 120 });
    await vi.waitFor(() =>
      expect(bindingMock.ReportFrontendPerf).toHaveBeenCalled(),
    );
    expect(bindingMock.ReportFrontendPerf).toHaveBeenLastCalledWith([
      {
        name: 'inp',
        value: 120,
        unit: 'ms',
        labels: {
          surface: 'main',
          route: 'chat',
          build: '9.9.9-test',
          conversation_id: 'c-42',
        },
      },
    ]);
  });
});
