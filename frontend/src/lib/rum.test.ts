import { describe, expect, it, vi } from 'vitest';
import { navigationSamples } from './rum';

vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => ({ ReportFrontendPerf: vi.fn() }),
);

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
});
