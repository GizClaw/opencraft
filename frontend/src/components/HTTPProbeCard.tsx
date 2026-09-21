import { Timer } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { HTTPProbeStatus } from '../lib/types';
import { ICON } from './ui/icon';
import { Segmented } from './ui/Segmented';

// HTTPProbeCard is the DEV switch for the provider round-trip probe:
// two records per provider call ("request dispatched" with the request
// size, "response headers received" with the status and the wait),
// which is the split between local assembly and provider time that the
// per-turn latency numbers cannot show. The streamable-HTTP MCP client
// cannot run under a wrapped transport, so an HTTP MCP server parks the
// probe; the card says so instead of pretending the switch took effect.
// showTitle is off when the card sits under a section heading that
// already carries the same name.
export function HTTPProbeCard({ showTitle = true }: { showTitle?: boolean }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<HTTPProbeStatus | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    void (async () => {
      try {
        setStatus(await api.httpProbe());
      } catch (err) {
        setError(String(err));
      }
    })();
  }, []);

  const toggle = async (next: boolean) => {
    const previous = status;
    if (previous) setStatus({ ...previous, enabled: next });
    setError('');
    try {
      setStatus(await api.setHTTPProbe(next));
    } catch (err) {
      setStatus(previous);
      setError(String(err));
    }
  };

  if (!status) {
    return error ? (
      <p className="text-xs text-err break-words">{error}</p>
    ) : null;
  }

  return (
    <div className="rounded-card border border-edge bg-panel2 p-3">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          {showTitle && (
            <div className="flex items-center gap-2 text-sm font-medium">
              <Timer size={ICON.sm} className="text-accent" />
              {t('config.diagHttpProbeTitle')}
            </div>
          )}
          <p className="mt-1 text-xs text-dim">
            {t('config.diagHttpProbeHint')}
          </p>
          <p className="mt-1 text-micro text-faint">
            {t('config.diagHttpProbeSeries')}
          </p>
          {status.active && (
            <p className="mt-1 text-micro text-ok">
              {t('config.diagHttpProbeActive')}
            </p>
          )}
          {status.env && (
            <p className="mt-1 text-micro text-warn">
              {t('config.diagHttpProbeEnv')}
            </p>
          )}
          {status.blocker && (
            <p className="mt-1 text-micro text-warn">
              {t('config.diagHttpProbeBlocked')} · {status.blocker}
            </p>
          )}
        </div>
        <Segmented
          value={status.enabled ? 'on' : 'off'}
          onChange={(next) => void toggle(next === 'on')}
          options={[
            {
              value: 'on',
              label: t('config.diagHttpProbeOn'),
              // The env var is the harder opt-in: a switch that lies
              // about turning the probe off is worse than a locked one.
              disabled: status.env,
            },
            {
              value: 'off',
              label: t('config.diagHttpProbeOff'),
              disabled: status.env,
            },
          ]}
        />
      </div>
      {error && (
        <p className="mt-1.5 text-label text-err break-words">{error}</p>
      )}
    </div>
  );
}
