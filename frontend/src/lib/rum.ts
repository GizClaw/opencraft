import { onFID, onINP } from 'web-vitals';
import type { Metric } from 'web-vitals';

import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';
import { buildVersion, reportLabels } from './perfLabels';

// PerfSample mirrors the Go FrontendPerfSample binding model.
export interface PerfSample {
  name: string;
  value: number;
  unit: string;
}

let started = false;
const lastValue = new Map<string, number>();

// send stamps one sample with the same labels the probe uses (surface,
// build, active conversation), resolved per sample so a report carries the
// dimensions of the moment it was taken.
async function send(sample: PerfSample) {
  const labels = reportLabels(await buildVersion());
  try {
    await Diagnostics.ReportFrontendPerf([{ ...sample, labels }]);
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
    // One navigation is read up to four times (before load, right after it,
    // a second later, and at hide): an unchanged value is not written again,
    // so a page load contributes one sample per event.
    const key = `nav:${sample.name}`;
    if (lastValue.get(key) === sample.value) continue;
    lastValue.set(key, sample.value);
    void send(sample);
  }
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
  //
  // LCP is deliberately not collected either: this window is an always-open
  // SPA, and the running largest-paint value keeps being raised by content
  // that renders long after startup — the stored series ran from ~0.3s to
  // minutes — so it measured how large the last render got, not how fast
  // the shell loads. Startup is covered by dom_content_loaded/load,
  // interactivity by FID/INP.
  //
  // CLS is deliberately not collected: web-vitals only arms it on engines
  // whose PerformanceObserver lists the layout-shift entry type, and WebKit
  // (the shell's engine on macOS and Linux) does not, so the callback could
  // never fire and the chart would sit empty.
  onFID((metric) => report(metric, 'ms'));
  onINP((metric) => report(metric, 'ms'));
}
