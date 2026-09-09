import { onCLS, onFID, onINP, onLCP, onTTFB } from 'web-vitals';
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

function reportNavigationTiming() {
  try {
    const entries = performance.getEntriesByType('navigation');
    const nav = entries[entries.length - 1] as
      PerformanceNavigationTiming | undefined;
    if (!nav) return;
    if (nav.domContentLoadedEventEnd > 0) {
      void send({
        name: 'dom_content_loaded',
        value: nav.domContentLoadedEventEnd - nav.startTime,
        unit: 'ms',
      });
    }
    if (nav.loadEventEnd > 0) {
      void send({
        name: 'load',
        value: nav.loadEventEnd - nav.startTime,
        unit: 'ms',
      });
    }
  } catch {
    // Unsupported performance APIs are ignored.
  }
}

// startRUM wires web-vitals and navigation timing once per page load. It is
// safe to call before the app shell mounts; unsupported metrics silently
// produce no observations.
export function startRUM(): void {
  if (started) return;
  started = true;

  reportNavigationTiming();
  onTTFB((metric) => report(metric, 'ms'));
  onLCP((metric) => report(metric, 'ms'));
  onFID((metric) => report(metric, 'ms'));
  onINP((metric) => report(metric, 'ms'));
  onCLS((metric) => report(metric, ''));
}
