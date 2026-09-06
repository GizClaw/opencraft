import type { ReactNode } from 'react';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Activity,
  ArrowDownToLine,
  ArrowUpFromLine,
  Database,
  LayoutGrid,
  Sparkles,
  Zap,
} from 'lucide-react';
import type { ModelUsageStat } from '../lib/types';

// Palette mirrors the trend chart (cc-switch style): input blue,
// output green, cache write orange, cache read purple.
const ACCENT_BLUE = '#3b82f6';
const ACCENT_GREEN = '#22c55e';
const ACCENT_ORANGE = '#f97316';
const ACCENT_PURPLE = '#a855f7';
const ACCENT_EMERALD = '#10b981';

function fmtShort(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

function MiniStat({
  icon,
  label,
  value,
  color,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  color: string;
}) {
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-edge/60 bg-panel/50 p-3 shadow-sm">
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-dim">
        <span style={{ color }}>{icon}</span>
        <span className="tracking-wide">{label}</span>
      </div>
      <div className="text-sm font-semibold tabular-nums text-fg">{value}</div>
    </div>
  );
}

export function UsageHero({
  rows,
  sessions,
}: {
  rows: ModelUsageStat[];
  sessions: number;
}) {
  const { t } = useTranslation();
  const totals = useMemo(() => {
    let input = 0;
    let output = 0;
    let cacheRead = 0;
    let cacheWrite = 0;
    let totalTokens = 0;
    let updatedAt = '';
    for (const r of rows) {
      input += r.input_tokens;
      output += r.output_tokens;
      cacheRead += r.cache_read_tokens;
      cacheWrite += r.cache_write_tokens;
      totalTokens += r.total_tokens;
      if (!updatedAt || r.updated_at > updatedAt) updatedAt = r.updated_at;
    }
    return {
      input,
      output,
      cacheRead,
      cacheWrite,
      // Provider-reported totals are authoritative; rows rebuilt from
      // legacy data fall back to input + output.
      total: totalTokens > 0 ? totalTokens : input + output,
      models: rows.length,
      updatedAt,
    };
  }, [rows]);

  const hitRate =
    totals.input > 0
      ? Math.min(100, Math.max(0, (totals.cacheRead / totals.input) * 100))
      : 0;
  const hitLabel = hitRate.toFixed(hitRate >= 99.95 ? 0 : 1);
  const mainNumber = totals.total.toLocaleString();
  const lastUsed = totals.updatedAt
    ? new Date(totals.updatedAt).toLocaleDateString()
    : '';

  return (
    <div className="usage-hero relative overflow-hidden rounded-xl border border-edge/60 bg-panel/60 p-4 shadow-sm backdrop-blur-sm md:p-5">
      <div
        aria-hidden
        className="pointer-events-none absolute -right-20 -top-28 h-56 w-72 rounded-full opacity-70 blur-3xl"
        style={{
          background:
            'radial-gradient(closest-side, rgba(59,130,246,0.14), transparent)',
        }}
      />
      <div className="relative flex flex-col gap-4">
        <div className="flex flex-col justify-between gap-4 md:flex-row md:items-center">
          <div className="flex items-center gap-3">
            <div
              className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl shadow-sm"
              style={{
                background:
                  'linear-gradient(135deg, rgba(59,130,246,0.18), rgba(59,130,246,0.05))',
              }}
            >
              <Zap size={20} style={{ color: ACCENT_BLUE }} />
            </div>
            <div>
              <div className="mb-0.5 flex items-center gap-1.5 text-[11px] font-medium text-dim">
                <span>{t('config.usageTokensTotal')}</span>
                <span className="text-dim/30">•</span>
                <span>{t('config.usageAllCumulative')}</span>
              </div>
              <div className="flex items-baseline gap-2">
                <span
                  className="text-2xl font-bold leading-none tracking-tight tabular-nums text-fg md:text-3xl"
                  title={mainNumber}
                >
                  {mainNumber}
                </span>
                <span className="rounded-md bg-panel px-1.5 py-0.5 text-xs font-medium text-dim">
                  ≈ {fmtShort(totals.total)}
                </span>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-5 rounded-xl border border-edge/60 bg-panel/60 px-4 py-2.5 shadow-sm">
            <div className="flex flex-col">
              <span className="text-[10px] font-medium uppercase tracking-wider text-dim">
                {t('config.usageSessions')}
              </span>
              <span className="flex items-center gap-1.5 text-sm font-semibold tabular-nums text-fg">
                <Activity size={14} style={{ color: ACCENT_BLUE }} />
                {sessions.toLocaleString()}
              </span>
            </div>
            <div className="h-8 w-px bg-edge/80" />
            <div className="flex flex-col">
              <span className="text-[10px] font-medium uppercase tracking-wider text-dim">
                {t('config.usageModels')}
              </span>
              <span className="flex items-center gap-1.5 text-sm font-semibold tabular-nums text-fg">
                <LayoutGrid size={14} style={{ color: ACCENT_PURPLE }} />
                {totals.models.toLocaleString()}
              </span>
            </div>
            {lastUsed && (
              <>
                <div className="h-8 w-px bg-edge/80" />
                <div className="flex flex-col">
                  <span className="text-[10px] font-medium uppercase tracking-wider text-dim">
                    {t('config.usageUpdated')}
                  </span>
                  <span className="text-sm font-semibold tabular-nums text-dim">
                    {lastUsed}
                  </span>
                </div>
              </>
            )}
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
          <MiniStat
            icon={<ArrowDownToLine size={14} />}
            label={t('config.usageInput')}
            value={fmtShort(totals.input)}
            color={ACCENT_BLUE}
          />
          <MiniStat
            icon={<ArrowUpFromLine size={14} />}
            label={t('config.usageOutput')}
            value={fmtShort(totals.output)}
            color={ACCENT_GREEN}
          />
          <MiniStat
            icon={<Database size={14} />}
            label={t('config.usageCacheWrite')}
            value={fmtShort(totals.cacheWrite)}
            color={ACCENT_ORANGE}
          />
          <MiniStat
            icon={<Sparkles size={14} />}
            label={t('config.usageCache')}
            value={fmtShort(totals.cacheRead)}
            color={ACCENT_PURPLE}
          />
          <div
            className="col-span-2 flex flex-col justify-center rounded-xl border border-edge/60 bg-panel/50 p-3 shadow-sm lg:col-span-1"
            title={t('config.usageCacheHitHint')}
          >
            <div className="mb-2 flex items-center justify-between text-[11px]">
              <span className="font-medium text-dim">
                {t('config.usageCacheHitRate')}
              </span>
              <span
                className="font-bold tabular-nums"
                style={{ color: ACCENT_EMERALD }}
              >
                {hitLabel}%
              </span>
            </div>
            <div className="relative h-1.5 overflow-hidden rounded-full bg-panel">
              <div
                className="usage-hit-fill absolute inset-y-0 left-0 rounded-full"
                style={{
                  backgroundColor: ACCENT_EMERALD,
                  ['--uc-hit-width' as string]: `${hitRate}%`,
                }}
              />
            </div>
            <div className="mt-1.5 text-[10px] tabular-nums text-dim">
              {fmtShort(totals.cacheRead)} / {fmtShort(totals.input)}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
