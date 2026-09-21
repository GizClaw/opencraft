import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { formatDateTime } from '../lib/datetime';
import { formatBytes } from '../lib/metricFormat';
import type { Recovery } from '../lib/types';

// The checkpoint table moves while turns run (one row per completed
// wave, dropped when the turn is archived), so the numbers are polled at
// the same cadence as the neighbouring command-pool card instead of read
// once.
const POLL_MS = 5000;

/**
 * RecoveryCard is the diagnostics view of crash recovery: what the pass
 * that runs at assembly recovered for the active workspace, and how much
 * its checkpoint table is holding now.
 *
 * The table should be near empty between turns — a row survives only for
 * a live run or an unarchived turn — so the counts double as the
 * write-amplification figure for per-wave checkpoints. The counters that
 * mean work was left on the table (discarded, failed, pending) are the
 * ones worth looking at, and they are the ones coloured.
 */
export function RecoveryCard() {
  const { t } = useTranslation();
  const [report, setReport] = useState<Recovery | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const next = await api.recovery();
      setReport(next);
      setError('');
    } catch (err) {
      setError(String(err));
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), POLL_MS);
    return () => window.clearInterval(timer);
  }, [load]);

  if (!report) {
    return error ? (
      <p className="text-xs text-err break-words">{error}</p>
    ) : null;
  }

  const stats: Array<{ label: string; value: number; attention?: boolean }> = [
    { label: t('config.diagRecoveryRecovered'), value: report.recovered },
    { label: t('config.diagRecoveryArchived'), value: report.archived },
    {
      label: t('config.diagRecoveryDiscarded'),
      value: report.discarded,
      attention: report.discarded > 0,
    },
    { label: t('config.diagRecoverySkippedLive'), value: report.skipped_live },
    {
      label: t('config.diagRecoveryFailed'),
      value: report.failed,
      attention: report.failed > 0,
    },
    {
      label: t('config.diagRecoveryPending'),
      value: report.pending,
      attention: report.pending > 0,
    },
  ];

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">
        {report.ran
          ? t('config.diagRecoveryLast', {
              at: formatDateTime(report.at ?? ''),
              recovered: report.recovered,
            })
          : t('config.diagRecoveryNone')}
      </p>
      {report.workspace && (
        <>
          <div className="grid grid-cols-3 gap-2">
            {stats.map((stat) => (
              <div
                key={stat.label}
                className="rounded-card border border-edge bg-panel2 px-2.5 py-2"
              >
                <div className="text-micro text-dim">{stat.label}</div>
                <div
                  className={`text-sm tabular-nums ${
                    stat.attention ? 'text-warn' : 'text-fg'
                  }`}
                >
                  {stat.value}
                </div>
              </div>
            ))}
          </div>
          <p className="text-micro text-faint">
            {t('config.diagRecoveryCheckpoints', {
              rows: report.checkpoint_rows,
              runs: report.checkpoint_runs,
              bytes: formatBytes(report.checkpoint_bytes),
            })}
          </p>
          <p className="break-all font-mono text-micro text-faint">
            {report.workspace}
          </p>
        </>
      )}
    </div>
  );
}
