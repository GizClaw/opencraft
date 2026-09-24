import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';
import { RefreshControl } from './RefreshControl';
import { CHART_AXIS, CHART_GRID, SERIES } from '../lib/chartPalette';
import { formatMetricSample } from '../lib/metricFormat';
import {
  downsample,
  type MetricAggregate,
  type MetricRow,
} from '../lib/metricSeries';

interface MetricPoint {
  ts: number;
  value: number;
  attrs?: Record<string, string>;
}

interface MetricDef {
  name: string;
  section: 'turn' | 'frontend' | 'probe' | 'memory' | 'gc' | 'proc';
  unit: string;
  // How samples fold when a range has more points than the chart draws.
  // A running value must stay a value the page actually had ('last'), a
  // per-event measurement averages, a worst-of-window takes the max.
  aggregate: MetricAggregate;
  split?: string;
}

type RangeKey = '1h' | '24h' | '7d' | 'all';

const RANGES: { key: RangeKey; ms: number | null; labelKey: string }[] = [
  { key: '1h', ms: 3_600_000, labelKey: 'config.metricsRange1h' },
  { key: '24h', ms: 86_400_000, labelKey: 'config.metricsRange24h' },
  { key: '7d', ms: 7 * 86_400_000, labelKey: 'config.metricsRange7d' },
  { key: 'all', ms: null, labelKey: 'config.metricsRangeAll' },
];

const METRICS: MetricDef[] = [
  {
    name: 'turn.duration_ms',
    section: 'turn',
    unit: 'ms',
    aggregate: 'mean',
    split: 'status',
  },
  {
    name: 'desktop.startup_ms',
    section: 'turn',
    unit: 'ms',
    aggregate: 'mean',
  },
  // Web-vitals series are running values: INP is the worst interaction so
  // far, FID the one first input of the session. Their samples are states,
  // not events. LCP is neither collected nor charted: this window is an
  // always-open SPA, and the running largest-paint value is raised by
  // whatever has painted so far (the stored series ran into minutes), so it
  // tracked how large the last render got rather than how fast the shell
  // loads (see rum.ts). CLS is not charted either: the shell's engine never
  // reports layout shifts.
  { name: 'frontend.inp', section: 'frontend', unit: 'ms', aggregate: 'last' },
  { name: 'frontend.fid', section: 'frontend', unit: 'ms', aggregate: 'last' },
  // One sample per page load, so a bucket holds a handful of real
  // measurements rather than a running state.
  {
    name: 'frontend.dom_content_loaded',
    section: 'frontend',
    unit: 'ms',
    aggregate: 'mean',
  },
  {
    name: 'frontend.load',
    section: 'frontend',
    unit: 'ms',
    aggregate: 'mean',
  },
  // The renderer probe (Diagnostics > DEV tools) reports once per 30s
  // window: how much time the window covers, three gauges taken at the
  // moment of the report (dom_nodes, conv_messages, mounted_rows), frame
  // counts for the window, and the worst values inside it. A report is
  // skipped while the window is hidden, so window_ms is what keeps frames
  // and the flush counts comparable across windows.
  {
    name: 'frontend.window_ms',
    section: 'probe',
    unit: 'ms',
    aggregate: 'last',
  },
  {
    name: 'frontend.dom_nodes',
    section: 'probe',
    unit: '',
    aggregate: 'last',
  },
  {
    name: 'frontend.conv_messages',
    section: 'probe',
    unit: '',
    aggregate: 'last',
  },
  {
    name: 'frontend.mounted_rows',
    section: 'probe',
    unit: '',
    aggregate: 'last',
  },
  { name: 'frontend.frames', section: 'probe', unit: '', aggregate: 'mean' },
  {
    name: 'frontend.frame_max',
    section: 'probe',
    unit: 'ms',
    aggregate: 'max',
  },
  // Frame gaps too long to be a frame: the window was occluded, the
  // machine asleep, or the renderer stalled past a second. Dropped from
  // the frame series next to this one, counted here so a window that was
  // not really sampled cannot read as a quiet one.
  {
    name: 'frontend.dropped_gaps',
    section: 'probe',
    unit: '',
    aggregate: 'sum',
  },
  // Frames whose gap blew 50 / 100 / 200ms in the report window: the
  // dropped-frame counts that work on every engine (the Chromium-only
  // longtask observer these replaced never fired under WebKit), and a
  // count of how many frames were that slow rather than only the worst.
  {
    name: 'frontend.long_frames_50',
    section: 'probe',
    unit: '',
    aggregate: 'sum',
  },
  {
    name: 'frontend.long_frames_100',
    section: 'probe',
    unit: '',
    aggregate: 'sum',
  },
  {
    name: 'frontend.long_frames_200',
    section: 'probe',
    unit: '',
    aggregate: 'sum',
  },
  {
    name: 'frontend.flush_p50',
    section: 'probe',
    unit: 'ms',
    aggregate: 'mean',
  },
  {
    name: 'frontend.flush_p95',
    section: 'probe',
    unit: 'ms',
    aggregate: 'mean',
  },
  {
    name: 'frontend.flush_max',
    section: 'probe',
    unit: 'ms',
    aggregate: 'max',
  },
  // Flushes inside that report window; a downsampled bucket sums to how
  // many flushes happened in it.
  {
    name: 'frontend.flush_count',
    section: 'probe',
    unit: '',
    aggregate: 'sum',
  },
  // The flush's other half: from the flush's start to the frame that
  // followed it — the routing itself plus everything else the frame had to
  // do before it painted. flush_p95 above is the synchronous part;
  // flush_commit_p95 is what the user waited for, so a renderer getting
  // more expensive shows up here before frame_max moves.
  {
    name: 'frontend.flush_commit_p50',
    section: 'probe',
    unit: 'ms',
    aggregate: 'mean',
  },
  {
    name: 'frontend.flush_commit_p95',
    section: 'probe',
    unit: 'ms',
    aggregate: 'mean',
  },
  {
    name: 'frontend.flush_commit_max',
    section: 'probe',
    unit: 'ms',
    aggregate: 'max',
  },
  // One markdown block's render-to-commit cost: the parse is what a long
  // answer makes the flush pay, and measuring it separately is what a
  // "render streaming markdown as plain text" decision would be based on.
  {
    name: 'frontend.flush_md_p95',
    section: 'probe',
    unit: 'ms',
    aggregate: 'mean',
  },
  // The slowest top-level interaction of the window (send, session switch,
  // opening or saving settings), labeled with which one it was. Only
  // windows that held an interaction carry the sample, so a gap in the
  // chart means nobody clicked rather than nothing measured.
  {
    name: 'frontend.interaction_max',
    section: 'probe',
    unit: 'ms',
    aggregate: 'max',
  },
  {
    name: 'go.mem.heap_alloc',
    section: 'memory',
    unit: 'B',
    aggregate: 'last',
  },
  { name: 'go.mem.heap_sys', section: 'memory', unit: 'B', aggregate: 'last' },
  {
    name: 'go.mem.heap_objects',
    section: 'memory',
    unit: '',
    aggregate: 'last',
  },
  {
    name: 'go.mem.alloc_bytes',
    section: 'memory',
    unit: 'B',
    aggregate: 'last',
  },
  { name: 'go.mem.alloc_ops', section: 'memory', unit: '', aggregate: 'last' },
  { name: 'go.gc.count', section: 'gc', unit: '', aggregate: 'last' },
  {
    name: 'go.gc.pause_total_ms',
    section: 'gc',
    unit: 'ms',
    aggregate: 'last',
  },
  {
    name: 'go.runtime.goroutines',
    section: 'gc',
    unit: '',
    aggregate: 'last',
  },
  // The app as the machine sees it, sampled every 15s next to the Go
  // gauges: family_total is every process this app is running, and
  // footprint breaks it down by role — self (the Go host process), child
  // (everything it spawned: sandbox children, MCP servers, plugin
  // binaries) and the platform helpers that run on its behalf while being
  // reparented to launchd (webcontent / gpu / networking). The renderers
  // are several times the Go heap and no Go gauge can see them, so this is
  // the series a window or a preview pane leaking into shows up in.
  {
    name: 'proc.mem.family_total',
    section: 'proc',
    unit: 'B',
    aggregate: 'last',
  },
  {
    name: 'proc.mem.footprint',
    section: 'proc',
    unit: 'B',
    aggregate: 'last',
    split: 'role',
  },
];

const SECTIONS: {
  key: MetricDef['section'];
  labelKey: string;
  hintKey?: string;
}[] = [
  { key: 'turn', labelKey: 'config.metricsSectionTurn' },
  {
    key: 'frontend',
    labelKey: 'config.metricsSectionFrontend',
    hintKey: 'config.metricsSectionFrontendHint',
  },
  {
    key: 'probe',
    labelKey: 'config.metricsSectionProbe',
    hintKey: 'config.metricsSectionProbeHint',
  },
  {
    key: 'proc',
    labelKey: 'config.metricsSectionProc',
    hintKey: 'config.metricsSectionProcHint',
  },
  { key: 'memory', labelKey: 'config.metricsSectionMemory' },
  { key: 'gc', labelKey: 'config.metricsSectionGC' },
];

const COLORS = SERIES;

// showHeading is off when the charts are rendered inside a dialog that
// already carries the title.
export function MetricsCharts({
  showHeading = true,
}: {
  showHeading?: boolean;
}) {
  const { t } = useTranslation();
  const [range, setRange] = useState<RangeKey>('24h');
  const [pointsByMetric, setPointsByMetric] = useState<
    Record<string, MetricPoint[]>
  >({});
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [pollMs, setPollMs] = useState(30_000);
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);

  const refresh = useCallback(
    async (silent: boolean) => {
      const id = ++seq.current;
      if (!silent) setLoading(true);
      setBusy(true);
      setFailed(false);
      const windowMs = RANGES.find((r) => r.key === range)?.ms ?? null;
      const from = windowMs === null ? 0 : Date.now() - windowMs;
      try {
        const entries = await Promise.all(
          METRICS.map(async (def) => {
            const points = await Diagnostics.MetricRange(
              def.name,
              from,
              0,
              5000,
            );
            return [def.name, (points ?? []) as MetricPoint[]] as const;
          }),
        );
        if (id !== seq.current) return;
        setPointsByMetric(Object.fromEntries(entries));
      } catch {
        if (id !== seq.current) return;
        setFailed(true);
      } finally {
        if (id === seq.current) {
          setBusy(false);
          if (!silent) setLoading(false);
        }
      }
    },
    [range],
  );

  useEffect(() => {
    void refresh(false);
  }, [refresh]);

  // Silent auto-refresh keeps the latest samples visible; like the Git panel
  // it re-arms after each poll completes so slow fetches never overlap.
  useEffect(() => {
    if (pollMs <= 0) return;
    let stopped = false;
    let timer: number | undefined;
    const tick = async () => {
      if (stopped) return;
      await refresh(true);
      if (!stopped) timer = window.setTimeout(tick, pollMs);
    };
    timer = window.setTimeout(tick, pollMs);
    return () => {
      stopped = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [refresh, pollMs]);

  const dateLabel = (ts: number): string => {
    const d = new Date(ts);
    return range === 'all' || range === '7d'
      ? d.toLocaleDateString()
      : d.toLocaleTimeString();
  };

  const renderMetric = (def: MetricDef) => {
    const points = pointsByMetric[def.name] ?? [];
    const splitValues = def.split
      ? Array.from(
          new Set(
            points
              .map((p) => p.attrs?.[def.split!])
              .filter((v): v is string => Boolean(v)),
          ),
        )
      : [];
    const rows: MetricRow[] = points.map((p) => {
      const key = def.split ? (p.attrs?.[def.split!] ?? 'unknown') : 'value';
      return { ts: p.ts, [key]: p.value } as MetricRow;
    });
    const seriesKeys = def.split
      ? splitValues.length > 0
        ? splitValues
        : ['value']
      : ['value'];
    const chartRows = downsample(rows, 400, def.aggregate);

    return (
      <div
        key={def.name}
        className="rounded-card border border-edge bg-panel2 px-3 py-2"
      >
        <div className="flex items-baseline justify-between gap-2">
          <span className="font-mono text-xs text-fg">{def.name}</span>
          <span className="text-micro text-dim">
            {def.unit || (def.split ? 'status' : '')}
          </span>
        </div>
        {chartRows.length === 0 ? (
          <p className="py-6 text-center text-xs text-dim">
            {t('config.metricsEmpty')}
          </p>
        ) : (
          <div className="h-36">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart
                data={chartRows}
                margin={{ top: 4, right: 4, bottom: 0, left: 0 }}
              >
                <CartesianGrid stroke={CHART_GRID} />
                <XAxis
                  dataKey="ts"
                  tickFormatter={dateLabel}
                  tick={{ fontSize: '0.7143rem', fill: CHART_AXIS }}
                  minTickGap={24}
                />
                <YAxis
                  tickFormatter={(v: number) => formatMetricSample(v, def.unit)}
                  tick={{ fontSize: '0.7143rem', fill: CHART_AXIS }}
                  width={52}
                />
                <Tooltip
                  labelFormatter={(ts) => dateLabel(Number(ts))}
                  formatter={(value, name) => {
                    const num =
                      typeof value === 'number' ? value : Number(value ?? 0);
                    return [
                      formatMetricSample(num, def.unit),
                      String(name ?? ''),
                    ];
                  }}
                  contentStyle={{
                    background: 'var(--color-panel3)',
                    border: '1px solid var(--color-edge)',
                    color: 'var(--color-fg)',
                    fontSize: '0.8571rem',
                  }}
                />
                {seriesKeys.map((key, i) => (
                  <Line
                    key={key}
                    type="monotone"
                    dataKey={key}
                    dot={false}
                    stroke={COLORS[i % COLORS.length]}
                    strokeWidth={1.5}
                    isAnimationActive={false}
                  />
                ))}
              </LineChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="space-y-3 border-t border-edge pt-3">
      <div className="flex items-center justify-between gap-2">
        {showHeading ? (
          <div>
            <p className="text-sm text-fg">{t('config.metricsTitle')}</p>
            <p className="text-xs text-dim">{t('config.metricsHint')}</p>
          </div>
        ) : (
          <p className="text-xs text-dim">{t('config.metricsHint')}</p>
        )}
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1">
            {RANGES.map((r) => (
              <button
                key={r.key}
                onClick={() => setRange(r.key)}
                className={`rounded-control border px-2 py-1 text-xs ${
                  range === r.key
                    ? 'border-accent/60 text-accent'
                    : 'border-edge text-dim hover:text-fg'
                }`}
              >
                {t(r.labelKey)}
              </button>
            ))}
          </div>
          <RefreshControl
            spinning={busy}
            value={pollMs}
            onChange={setPollMs}
            onRefresh={() => void refresh(false)}
            refreshLabel={t('config.metricsRefresh')}
            intervalLabel={t('config.metricsAutoRefresh')}
            offLabel={t('config.metricsOff')}
          />
        </div>
      </div>
      {failed && (
        <p className="text-xs text-err">{t('config.metricsLoadFailed')}</p>
      )}
      {loading ? (
        <p className="py-6 text-center text-xs text-dim">…</p>
      ) : (
        <div className="space-y-4">
          {SECTIONS.map((section) => {
            const defs = METRICS.filter((d) => d.section === section.key);
            return (
              <div key={section.key} className="space-y-2">
                <p className="text-xs font-medium text-dim">
                  {t(section.labelKey)}
                </p>
                {section.hintKey && (
                  <p className="text-micro text-faint">{t(section.hintKey)}</p>
                )}
                <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
                  {defs.map(renderMetric)}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
