import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { UsagePoint } from '../lib/types';

// Self-contained SVG trend chart (no chart library): one line per
// token stream, hover tooltip, time labels for hour/day buckets.

const W = 680;
const H = 300;
const ML = 46;
const MR = 14;
const MT = 14;
const MB = 30;

const SERIES: {
  key:
    'input_tokens' | 'output_tokens' | 'cache_read_tokens' | 'reasoning_tokens';
  labelKey: string;
  color: string;
}[] = [
  {
    key: 'input_tokens',
    labelKey: 'config.usageInput',
    color: 'var(--color-accent)',
  },
  {
    key: 'output_tokens',
    labelKey: 'config.usageOutput',
    color: 'var(--color-ok)',
  },
  {
    key: 'cache_read_tokens',
    labelKey: 'config.usageCache',
    color: 'var(--color-subagent)',
  },
  {
    key: 'reasoning_tokens',
    labelKey: 'config.usageReasoning',
    color: 'var(--color-warn)',
  },
];

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(n >= 100_000 ? 0 : 1)}k`;
  return String(n);
}

// niceCeil rounds a value up to a 1/2/2.5/5 x 10^n boundary so the
// axis ticks land on clean numbers.
function niceCeil(v: number): number {
  if (v <= 0) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = Math.pow(10, exp);
  const frac = v / base;
  const nice =
    frac <= 1 ? 1 : frac <= 2 ? 2 : frac <= 2.5 ? 2.5 : frac <= 5 ? 5 : 10;
  return nice * base;
}

// smoothLinePath builds a monotone cubic Hermite (Fritsch–Carlson)
// spline through the points — the same smooth "monotone" curve
// Recharts/cc-switch render — instead of a straight polyline.
function smoothLinePath(xs: number[], ys: number[]): string {
  const n = xs.length;
  if (n < 2) return '';
  if (n === 2) {
    return `M${xs[0].toFixed(2)} ${ys[0].toFixed(2)} L${xs[1].toFixed(2)} ${ys[1].toFixed(2)}`;
  }
  const h: number[] = [];
  const s: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    h[i] = xs[i + 1] - xs[i];
    s[i] = (ys[i + 1] - ys[i]) / h[i];
  }
  // Tangents at each point: centered slopes when the neighbors agree
  // in sign, zero at local extrema (keeps the curve monotone).
  const m = new Array<number>(n).fill(0);
  m[0] = s[0];
  m[n - 1] = s[n - 2];
  for (let i = 1; i < n - 1; i++) {
    m[i] = s[i - 1] * s[i] > 0 ? (s[i - 1] + s[i]) / 2 : 0;
  }
  // Fritsch–Carlson limiting prevents overshoot between segments.
  for (let i = 0; i < n - 1; i++) {
    if (s[i] === 0) continue;
    const alpha = m[i] / s[i];
    const beta = m[i + 1] / s[i];
    const d = alpha * alpha + beta * beta;
    if (d > 9) {
      const scale = 3 / Math.sqrt(d);
      m[i] *= scale;
      m[i + 1] *= scale;
    }
  }
  let d = `M${xs[0].toFixed(2)} ${ys[0].toFixed(2)}`;
  for (let i = 0; i < n - 1; i++) {
    const c1x = xs[i] + h[i] / 3;
    const c1y = ys[i] + (m[i] * h[i]) / 3;
    const c2x = xs[i + 1] - h[i] / 3;
    const c2y = ys[i + 1] - (m[i + 1] * h[i]) / 3;
    d += ` C${c1x.toFixed(2)} ${c1y.toFixed(2)},${c2x.toFixed(2)} ${c2y.toFixed(2)},${xs[i + 1].toFixed(2)} ${ys[i + 1].toFixed(2)}`;
  }
  return d;
}

function hourKey(ts: number): string {
  return `${new Date(ts).toISOString().slice(0, 13)}:00:00Z`;
}

function dayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${m}-${day}`;
}

function localMidnightMs(ts: number): number {
  const d = new Date(ts);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

function zeroPoint(time: string): UsagePoint {
  return {
    time,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    reasoning_tokens: 0,
  };
}

// fillUsageSeries zero-fills the entire [startMs, endMs] window the
// way cc-switch does: hourly buckets (aligned to whole hours) for
// short ranges, local-day buckets for longer ones, with zero buckets
// before the first record and after the last one so the timeline is
// continuous across the whole selection.
export function fillUsageSeries(
  points: UsagePoint[],
  granularity: 'hour' | 'day',
  startMs: number,
  endMs: number,
): UsagePoint[] {
  const byKey = new Map(points.map((p) => [p.time, p]));
  const out: UsagePoint[] = [];
  if (granularity === 'hour') {
    const first = Math.floor(startMs / 3_600_000) * 3_600_000;
    for (let ts = first; ts <= endMs; ts += 3_600_000) {
      const key = hourKey(ts);
      out.push(byKey.get(key) ?? zeroPoint(key));
    }
  } else {
    let d = new Date(localMidnightMs(startMs));
    const end = new Date(localMidnightMs(endMs));
    while (d.getTime() <= end.getTime()) {
      const key = dayKey(d);
      out.push(byKey.get(key) ?? zeroPoint(key));
      d = new Date(d.getTime());
      d.setDate(d.getDate() + 1);
    }
  }
  return out;
}

interface UsageChartProps {
  points: UsagePoint[];
  granularity: 'hour' | 'day';
  startMs: number;
  endMs: number;
  rangeLabel: string;
}

export function UsageChart({
  points,
  granularity,
  startMs,
  endMs,
  rangeLabel,
}: UsageChartProps) {
  const { t, i18n } = useTranslation();
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const locale = i18n.resolvedLanguage?.startsWith('zh') ? 'zh-CN' : 'en-US';
  const filled = useMemo(
    () => fillUsageSeries(points, granularity, startMs, endMs),
    [points, granularity, startMs, endMs],
  );

  const fmtTime = (iso: string, full = false): string => {
    if (granularity === 'day') {
      const d = new Date(`${iso}T00:00:00`);
      if (Number.isNaN(d.getTime())) return iso;
      return d.toLocaleDateString(
        locale,
        full
          ? { year: 'numeric', month: '2-digit', day: '2-digit' }
          : { month: '2-digit', day: '2-digit' },
      );
    }
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(locale, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  };

  const yMax = useMemo(
    () =>
      niceCeil(
        Math.max(
          1,
          ...filled.map(
            (p) =>
              p.input_tokens +
              p.output_tokens +
              p.cache_read_tokens +
              p.reasoning_tokens,
          ),
        ),
      ),
    [filled],
  );

  if (startMs <= 0 || endMs <= 0 || filled.length === 0) {
    return (
      <div className="grid h-[14.2857rem] place-items-center rounded-xl border border-edge bg-panel2 text-sm text-dim">
        {t('config.usageSeriesEmpty')}
      </div>
    );
  }

  const plotW = W - ML - MR;
  const plotH = H - MT - MB;
  const n = filled.length;
  const x = (i: number) =>
    n === 1 ? ML + plotW / 2 : ML + (i * plotW) / (n - 1);
  const y = (v: number) => MT + plotH - (v / yMax) * plotH;

  const tickIndices: number[] = [];
  const step = Math.max(1, Math.ceil((n - 1) / 7));
  for (let i = 0; i < n; i += step) tickIndices.push(i);
  if (tickIndices[tickIndices.length - 1] !== n - 1) tickIndices.push(n - 1);

  const linePath = (key: (typeof SERIES)[number]['key']) =>
    smoothLinePath(
      filled.map((_, i) => x(i)),
      filled.map((p) => y(p[key])),
    );

  const areaPath = (key: (typeof SERIES)[number]['key']) => {
    const baseY = MT + plotH;
    return `${linePath(key)} L${x(n - 1).toFixed(2)} ${baseY} L${x(0).toFixed(2)} ${baseY} Z`;
  };

  const maxAt = (i: number) =>
    Math.max(
      filled[i].input_tokens,
      filled[i].output_tokens,
      filled[i].cache_read_tokens,
      filled[i].reasoning_tokens,
    );

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width === 0) return;
    const ratio = Math.min(
      1,
      Math.max(0, (e.clientX - rect.left) / rect.width),
    );
    const idx = Math.round(ratio * (n - 1));
    setHoverIdx(idx);
  };

  const hover = hoverIdx !== null && hoverIdx < filled.length ? hoverIdx : null;
  const hoverPoint = hover !== null ? filled[hover] : null;

  return (
    <div className="rounded-xl border border-edge bg-panel2 p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold">{t('config.usageTrend')}</h3>
        <p className="text-xs text-dim">{rangeLabel}</p>
      </div>
      <div className="relative">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="block h-[21.4286rem] w-full cursor-crosshair"
          onMouseMove={onMove}
          onMouseLeave={() => setHoverIdx(null)}
        >
          <defs>
            {SERIES.map((s) => (
              <linearGradient
                key={s.key}
                id={`grad-${s.key}`}
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop offset="5%" stopColor={s.color} stopOpacity={0.25} />
                <stop offset="95%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          {[0, 1, 2, 3, 4].map((tick) => {
            const ty = MT + (tick * plotH) / 4;
            const value = yMax * (1 - tick / 4);
            return (
              <g key={tick}>
                <line
                  x1={ML}
                  x2={W - MR}
                  y1={ty}
                  y2={ty}
                  stroke="var(--color-edge)"
                  strokeOpacity={0.6}
                  strokeDasharray={tick === 0 ? undefined : '3 3'}
                />
                <text
                  x={ML - 8}
                  y={ty + 3.5}
                  textAnchor="end"
                  fontSize={10.5}
                  fill="var(--color-dim)"
                >
                  {fmtTokens(value)}
                </text>
              </g>
            );
          })}
          {tickIndices.map((i) => (
            <text
              key={i}
              x={x(i)}
              y={H - MB + 16}
              textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}
              fontSize={10.5}
              fill="var(--color-dim)"
            >
              {fmtTime(filled[i].time)}
            </text>
          ))}
          {SERIES.map((s) => (
            <g key={s.key}>
              <path
                d={areaPath(s.key)}
                fill={`url(#grad-${s.key})`}
                stroke="none"
              />
              <path
                d={linePath(s.key)}
                fill="none"
                stroke={s.color}
                strokeWidth={2}
                strokeLinejoin="round"
                strokeLinecap="round"
              />
            </g>
          ))}
          {n === 1 &&
            SERIES.map((s) => (
              <circle
                key={s.key}
                cx={x(0)}
                cy={y(filled[0][s.key])}
                r={3}
                fill={s.color}
              />
            ))}
          {hover !== null && (
            <>
              <line
                x1={x(hover)}
                x2={x(hover)}
                y1={MT}
                y2={MT + plotH}
                stroke="var(--color-dim)"
                strokeOpacity={0.5}
                strokeDasharray="3 3"
              />
              {SERIES.map((s) => (
                <circle
                  key={s.key}
                  cx={x(hover)}
                  cy={y(filled[hover][s.key])}
                  r={3}
                  fill={s.color}
                  stroke="var(--color-panel2)"
                />
              ))}
            </>
          )}
        </svg>
        {hover !== null && hoverPoint && (
          <div
            className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-xs shadow-xl"
            style={{
              left: `${(x(hover) / W) * 100}%`,
              top: `${(y(maxAt(hover)) / H) * 100}%`,
            }}
          >
            <p className="mb-1 whitespace-nowrap text-dim">
              {fmtTime(hoverPoint.time, true)}
            </p>
            {SERIES.map((s) => (
              <div
                key={s.key}
                className="flex items-center gap-1.5 whitespace-nowrap"
              >
                <span
                  className="h-2 w-2 rounded-full"
                  style={{ backgroundColor: s.color }}
                />
                <span>{t(s.labelKey)}</span>
                <span className="ml-auto pl-3 tabular-nums">
                  {fmtTokens(hoverPoint[s.key])}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-dim">
        {SERIES.map((s) => (
          <span key={s.key} className="flex items-center gap-1.5">
            <span
              className="h-2 w-2 rounded-full"
              style={{ backgroundColor: s.color }}
            />
            {t(s.labelKey)}
          </span>
        ))}
      </div>
    </div>
  );
}
