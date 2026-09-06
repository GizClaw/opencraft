import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { UsagePoint } from '../lib/types';
import {
  ceilHourMs,
  ceilLocalDayMs,
  floorHourMs,
  localMidnightMs,
} from '../lib/usageWindow';

// Chart palette mirrors cc-switch's usage trend: input blue, output
// green, cache write orange, cache read purple, reasoning red dashed
// (like cc-switch's cost overlay).
const STREAMS: {
  key:
    | 'input_tokens'
    | 'output_tokens'
    | 'cache_write_tokens'
    | 'cache_read_tokens'
    | 'reasoning_tokens';
  color: string;
  labelKey: string;
  dashed?: boolean;
}[] = [
  {
    key: 'input_tokens',
    color: '#3b82f6',
    labelKey: 'config.usageInput',
  },
  {
    key: 'output_tokens',
    color: '#22c55e',
    labelKey: 'config.usageOutput',
  },
  {
    key: 'cache_write_tokens',
    color: '#f97316',
    labelKey: 'config.usageCacheWrite',
  },
  {
    key: 'cache_read_tokens',
    color: '#a855f7',
    labelKey: 'config.usageCache',
  },
  {
    key: 'reasoning_tokens',
    color: '#f43f5e',
    labelKey: 'config.usageReasoning',
    dashed: true,
  },
];

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(n >= 100_000 ? 0 : 1)}k`;
  return String(n);
}

function hourKey(ts: number): string {
  return `${new Date(ts).toISOString().slice(0, 13)}:00:00Z`;
}

function dayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${m}-${day}`;
}

function formatBucketTime(
  iso: string,
  granularity: 'hour' | 'day',
  locale: string,
  full = false,
): string {
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
}

function zeroPoint(time: string): UsagePoint {
  return {
    time,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    reasoning_tokens: 0,
  };
}

// fillUsageSeries zero-fills the buckets of an [startMs, endMs]
// window. It mirrors alignUsageWindow: the first bucket is the whole
// hour (or local day) containing startMs and the last is the one
// containing endMs, exclusive of the aligned end. The set of buckets
// must match the backend Series filter, which includes a bucket when
// its start instant lies in [start, end).
export function fillUsageSeries(
  points: UsagePoint[],
  granularity: 'hour' | 'day',
  startMs: number,
  endMs: number,
): UsagePoint[] {
  const byKey = new Map(points.map((p) => [p.time, p]));
  const out: UsagePoint[] = [];
  if (granularity === 'hour') {
    const first = floorHourMs(startMs);
    const end = ceilHourMs(endMs);
    for (let ts = first; ts < end; ts += 3_600_000) {
      const key = hourKey(ts);
      out.push(byKey.get(key) ?? zeroPoint(key));
    }
  } else {
    let d = new Date(localMidnightMs(startMs));
    const end = ceilLocalDayMs(endMs);
    while (d.getTime() < end) {
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

interface ChartRow extends UsagePoint {
  shortLabel: string;
  fullLabel: string;
}

export function UsageChart({
  points,
  granularity,
  startMs,
  endMs,
  rangeLabel,
}: UsageChartProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage?.startsWith('zh') ? 'zh-CN' : 'en-US';
  const filled = useMemo(
    () => fillUsageSeries(points, granularity, startMs, endMs),
    [points, granularity, startMs, endMs],
  );

  const chartData = useMemo<ChartRow[]>(
    () =>
      filled.map((p) => ({
        ...p,
        shortLabel: formatBucketTime(p.time, granularity, locale),
        fullLabel: formatBucketTime(p.time, granularity, locale, true),
      })),
    [filled, granularity, locale],
  );

  const compactFormatter = useMemo(
    () =>
      new Intl.NumberFormat(locale, {
        notation: 'compact',
        compactDisplay: 'short',
        maximumFractionDigits: 1,
      }),
    [locale],
  );

  if (startMs <= 0 || endMs <= 0 || filled.length === 0) {
    return (
      <div className="grid h-[18rem] place-items-center rounded-xl border border-edge/70 bg-panel/70 text-sm text-dim backdrop-blur-sm">
        {t('config.usageSeriesEmpty')}
      </div>
    );
  }

  const CustomTooltip = ({
    active,
    payload,
  }: {
    active?: boolean;
    payload?: Array<{ payload: ChartRow; color: string; dataKey: string }>;
  }) => {
    if (!active || !payload || payload.length === 0) return null;
    const point = payload[0]?.payload;
    return (
      <div className="rounded-lg border border-edge bg-panel/95 p-3 shadow-lg backdrop-blur-md">
        <p className="mb-2 font-medium text-fg">{point?.fullLabel}</p>
        {payload.map((entry) => {
          const stream = STREAMS.find((s) => s.key === entry.dataKey);
          if (!stream) return null;
          const value = (entry.payload as UsagePoint)[stream.key] as number;
          return (
            <div
              key={stream.key}
              className="flex items-center gap-2 text-sm"
              style={{ color: stream.color }}
            >
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: stream.color }}
              />
              <span className="font-medium text-fg">{t(stream.labelKey)}</span>
              <span className="ml-auto pl-3 tabular-nums">
                {fmtTokens(value)}
              </span>
            </div>
          );
        })}
      </div>
    );
  };

  return (
    <div className="rounded-xl border border-edge/60 bg-panel/60 p-4 backdrop-blur-sm md:p-6">
      <div className="mb-4 flex items-center justify-between md:mb-6">
        <h3 className="text-base font-semibold text-fg">
          {t('config.usageTrend')}
        </h3>
        <p className="text-xs text-dim">{rangeLabel}</p>
      </div>
      <div className="h-[21.4286rem] w-full">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart
            data={chartData}
            margin={{ top: 10, right: 10, left: 0, bottom: 0 }}
          >
            <defs>
              {STREAMS.filter((s) => !s.dashed).map((s) => (
                <linearGradient
                  key={s.key}
                  id={`usage-grad-${s.key}`}
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="1"
                >
                  <stop offset="5%" stopColor={s.color} stopOpacity={0.22} />
                  <stop offset="95%" stopColor={s.color} stopOpacity={0} />
                </linearGradient>
              ))}
            </defs>
            <CartesianGrid
              strokeDasharray="3 3"
              vertical={false}
              stroke="var(--color-edge)"
              opacity={0.45}
            />
            <XAxis
              dataKey="time"
              axisLine={false}
              tickLine={false}
              tick={{ fill: 'var(--color-dim)', fontSize: 12 }}
              dy={10}
              minTickGap={28}
              tickFormatter={(value: string) =>
                chartData.find((p) => p.time === value)?.shortLabel ??
                String(value)
              }
            />
            <YAxis
              width={64}
              axisLine={false}
              tickLine={false}
              tickMargin={8}
              tick={{ fill: 'var(--color-dim)', fontSize: 12 }}
              tickFormatter={(value: number) => compactFormatter.format(value)}
            />
            <Tooltip
              content={<CustomTooltip />}
              cursor={{
                stroke: 'var(--color-dim)',
                strokeOpacity: 0.5,
                strokeDasharray: '3 3',
              }}
            />
            {STREAMS.map((s) => (
              <Area
                key={s.key}
                type="monotone"
                dataKey={s.key}
                name={t(s.labelKey)}
                stroke={s.color}
                strokeWidth={2}
                strokeDasharray={s.dashed ? '5 5' : undefined}
                fillOpacity={1}
                fill={s.dashed ? 'none' : `url(#usage-grad-${s.key})`}
                activeDot={{ r: 4, strokeWidth: 0 }}
                animationDuration={900}
              />
            ))}
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-dim">
        {STREAMS.map((s) => (
          <span key={s.key} className="flex items-center gap-1.5">
            <span
              className="inline-block rounded-full"
              style={{
                width: s.dashed ? 14 : 8,
                height: s.dashed ? 0 : 8,
                borderTop: s.dashed ? `2px dashed ${s.color}` : undefined,
                backgroundColor: s.dashed ? 'transparent' : s.color,
              }}
            />
            {t(s.labelKey)}
          </span>
        ))}
      </div>
    </div>
  );
}
