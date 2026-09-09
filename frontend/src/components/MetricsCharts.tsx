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

interface MetricPoint {
  ts: number;
  value: number;
  attrs?: Record<string, string>;
}

interface MetricDef {
  name: string;
  section: 'turn' | 'frontend' | 'memory' | 'gc';
  unit: string;
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
  { name: 'turn.duration_ms', section: 'turn', unit: 'ms', split: 'status' },
  { name: 'desktop.startup_ms', section: 'turn', unit: 'ms' },
  { name: 'frontend.lcp', section: 'frontend', unit: 'ms' },
  { name: 'frontend.inp', section: 'frontend', unit: 'ms' },
  { name: 'frontend.fid', section: 'frontend', unit: 'ms' },
  { name: 'frontend.ttfb', section: 'frontend', unit: 'ms' },
  { name: 'frontend.dom_content_loaded', section: 'frontend', unit: 'ms' },
  { name: 'frontend.load', section: 'frontend', unit: 'ms' },
  { name: 'frontend.cls', section: 'frontend', unit: '' },
  { name: 'go.mem.heap_alloc', section: 'memory', unit: 'B' },
  { name: 'go.mem.heap_sys', section: 'memory', unit: 'B' },
  { name: 'go.mem.heap_objects', section: 'memory', unit: '' },
  { name: 'go.mem.alloc_bytes', section: 'memory', unit: 'B' },
  { name: 'go.mem.alloc_ops', section: 'memory', unit: '' },
  { name: 'go.gc.count', section: 'gc', unit: '' },
  { name: 'go.gc.pause_total_ms', section: 'gc', unit: 'ms' },
  { name: 'go.runtime.goroutines', section: 'gc', unit: '' },
];

const SECTIONS: {
  key: MetricDef['section'];
  labelKey: string;
}[] = [
  { key: 'turn', labelKey: 'config.metricsSectionTurn' },
  { key: 'frontend', labelKey: 'config.metricsSectionFrontend' },
  { key: 'memory', labelKey: 'config.metricsSectionMemory' },
  { key: 'gc', labelKey: 'config.metricsSectionGC' },
];

const COLORS = ['#60a5fa', '#34d399', '#fbbf24', '#f87171', '#a78bfa'];

function compactNumber(value: number): string {
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (Math.abs(value) >= 1_000) return `${(value / 1_000).toFixed(1)}k`;
  return String(Math.round(value * 100) / 100);
}

function fmtBytes(value: number): string {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(1)} GB`;
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${value} B`;
}

function fmtAxisValue(value: number, unit: string): string {
  return unit === 'B' ? fmtBytes(value) : compactNumber(value);
}

interface Row {
  ts: number;
  [series: string]: number;
}

function downsample(rows: Row[], maxPoints: number): Row[] {
  if (rows.length <= maxPoints) return rows;
  const chunk = Math.ceil(rows.length / maxPoints);
  const out: Row[] = [];
  for (let i = 0; i < rows.length; i += chunk) {
    const part = rows.slice(i, i + chunk);
    const merged: Row = { ts: part[0].ts };
    for (const series of Object.keys(part[0])) {
      if (series === 'ts') continue;
      merged[series] =
        part.reduce((sum, row) => sum + (row[series] ?? 0), 0) / part.length;
    }
    out.push(merged);
  }
  return out;
}

export function MetricsCharts() {
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
    const rows: Row[] = points.map((p) => {
      const key = def.split ? (p.attrs?.[def.split!] ?? 'unknown') : 'value';
      return { ts: p.ts, [key]: p.value } as Row;
    });
    const seriesKeys = def.split
      ? splitValues.length > 0
        ? splitValues
        : ['value']
      : ['value'];
    const chartRows = downsample(rows, 400);

    return (
      <div
        key={def.name}
        className="rounded-lg border border-edge bg-panel2 px-3 py-2"
      >
        <div className="flex items-baseline justify-between gap-2">
          <span className="font-mono text-xs text-fg">{def.name}</span>
          <span className="text-[0.6875rem] text-dim">
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
                <CartesianGrid stroke="rgba(148,163,184,0.15)" />
                <XAxis
                  dataKey="ts"
                  tickFormatter={dateLabel}
                  tick={{ fontSize: 10, fill: '#94a3b8' }}
                  minTickGap={24}
                />
                <YAxis
                  tickFormatter={(v: number) => fmtAxisValue(v, def.unit)}
                  tick={{ fontSize: 10, fill: '#94a3b8' }}
                  width={52}
                />
                <Tooltip
                  labelFormatter={(ts) => dateLabel(Number(ts))}
                  formatter={(value, name) => {
                    const num =
                      typeof value === 'number' ? value : Number(value ?? 0);
                    return [
                      def.unit === 'B'
                        ? fmtBytes(num)
                        : `${fmtAxisValue(num, def.unit)} ${def.unit}`.trim(),
                      String(name ?? ''),
                    ];
                  }}
                  contentStyle={{
                    background: '#0f172a',
                    border: '1px solid #334155',
                    fontSize: 12,
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
        <div>
          <p className="text-sm text-fg">{t('config.metricsTitle')}</p>
          <p className="text-xs text-dim">{t('config.metricsHint')}</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1">
            {RANGES.map((r) => (
              <button
                key={r.key}
                onClick={() => setRange(r.key)}
                className={`rounded-md border px-2 py-1 text-xs ${
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
