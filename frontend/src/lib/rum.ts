import { onCLS, onFID, onINP, onLCP } from 'web-vitals';
import type { Metric } from 'web-vitals';

import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';

// PerfSample mirrors the Go FrontendPerfSample binding model.
export interface PerfSample {
  name: string;
  value: number;
  unit: string;
}

let started = false;
const lastValue = new Map<string, number>();

async function send(sample: PerfSample) {
  try {
    await Diagnostics.ReportFrontendPerf([sample]);
  } catch {
    // RUM is best-effort: a dead binding must not affect the UI.
  }
}

// report forwards one web-vitals observation. web-vitals may deliver several
// estimates for the same metric id (INP improves over the session, CLS grows
// until the page is hidden); unchanged values are deduplicated.
function report(metric: Metric, unit: string) {
  const name = metric.name.toLowerCase();
  const key = `${metric.id}:${name}`;
  const previous = lastValue.get(key);
  if (previous !== undefined && metric.value === previous) return;
  lastValue.set(key, metric.value);
  void send({ name, value: metric.value, unit });
}

// navigationSamples turns one navigation entry into the samples worth
// sending. A zeroed field means the event has not happened yet (the first
// pass runs while the page is still loading) or that the platform does not
// report it: recording that zero would read as "the page loaded instantly"
// on the charts.
export function navigationSamples(
  nav: PerformanceNavigationTiming,
): PerfSample[] {
  const out: PerfSample[] = [];
  if (nav.domContentLoadedEventEnd > 0) {
    out.push({
      name: 'dom_content_loaded',
      value: nav.domContentLoadedEventEnd - nav.startTime,
      unit: 'ms',
    });
  }
  if (nav.loadEventEnd > 0) {
    out.push({
      name: 'load',
      value: nav.loadEventEnd - nav.startTime,
      unit: 'ms',
    });
  }
  return out;
}

function latestNavigation(): PerformanceNavigationTiming | undefined {
  try {
    const entries = performance.getEntriesByType('navigation');
    return entries[entries.length - 1] as
      PerformanceNavigationTiming | undefined;
  } catch {
    // Unsupported performance APIs are ignored.
    return undefined;
  }
}

function reportNavigationTiming() {
  const nav = latestNavigation();
  if (!nav) return;
  for (const sample of navigationSamples(nav)) {
    void send(sample);
  }
}

// reportCLS keeps CLS a series instead of a single end-of-session number:
// web-vitals only reports it once the document is hidden, which a desktop
// window may never do. Steps smaller than the threshold are folded together
// so a chatty page does not write one row per layout shift.
function reportCLS(metric: Metric) {
  const previous = lastValue.get('cls-reported');
  const hidden = document.visibilityState === 'hidden';
  // The first observation is sent even when it is 0: a cumulative shift of
  // zero is the answer for a page that did not move, and dropping it left
  // the chart empty for the sessions that behaved best.
  const grew = previous === undefined || metric.value - previous >= 0.005;
  if (!grew && !hidden) return;
  lastValue.set('cls-reported', metric.value);
  report(metric, '');
}

// startRUM wires web-vitals and navigation timing once per page load. It is
// safe to call before the app shell mounts; unsupported metrics silently
// produce no observations.
export function startRUM(): void {
  if (started) return;
  started = true;

  // The first pass runs before domContentLoaded/load have finished, so the
  // load event is what records those two. loadEventEnd is only stamped when
  // the load event *finishes*, so the entry has to be read after that task,
  // and a second look covers an engine that fills it in later. A page that
  // is already complete (the bundle can be evaluated after the event) never
  // sees the listener, so it reports on the spot.
  reportNavigationTiming();
  const afterLoad = () => {
    window.setTimeout(() => {
      reportNavigationTiming();
      window.setTimeout(reportNavigationTiming, 1000);
    }, 0);
  };
  if (document.readyState === 'complete') {
    afterLoad();
  } else {
    window.addEventListener('load', afterLoad, { once: true });
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      reportNavigationTiming();
    }
  });

  // TTFB is deliberately not collected: the app's own pages are served over
  // the custom wails:// scheme, whose navigation entry carries no response
  // timing (responseStart stays 0), so web-vitals never reports it. The
  // chart was removed with it rather than left permanently empty.
  onLCP((metric) => report(metric, 'ms'));
  onFID((metric) => report(metric, 'ms'));
  onINP((metric) => report(metric, 'ms'));
  onCLS(reportCLS, { reportAllChanges: true });
}
