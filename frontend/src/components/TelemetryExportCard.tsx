import { Events } from '@wailsio/runtime';
import { Radio } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { TelemetryExportStatus, UIEvent } from '../lib/types';
import { ICON } from './ui/icon';
import { Segmented } from './ui/Segmented';

// TelemetryExportCard is the diagnostics view of the OTLP export sink:
// where the app currently ships logs, traces and metrics, and whether
// capability plugins that declare telemetry:export may point that export
// at their own collector. The local rotating log file is unaffected by
// either, which the hint spells out.
export function TelemetryExportCard() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<TelemetryExportStatus | null>(null);
  const [error, setError] = useState('');

  const load = async () => {
    try {
      setStatus(await api.telemetryExport());
      setError('');
    } catch (err) {
      setError(String(err));
    }
  };

  useEffect(() => {
    void load();
  }, []);

  // The host emits telemetry_changed whenever a plugin installs, drops or
  // is refused an export sink, including when the switch itself changes.
  useEffect(() => {
    const off = Events.On('opencraft:ui', (e) => {
      const ev = e.data as UIEvent;
      if (ev.type === 'telemetry_changed') void load();
    });
    return off;
  }, []);

  const setEnabled = async (enabled: boolean) => {
    const previous = status;
    if (previous) setStatus({ ...previous, enabled });
    try {
      await api.setTelemetryExport(enabled);
      await load();
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
          <div className="flex items-center gap-2 text-sm font-medium">
            <Radio size={ICON.sm} className="text-accent" />
            {t('config.diagTelemetryTitle')}
          </div>
          <p className="mt-1 text-xs text-dim">
            {t('config.diagTelemetryHint')}
          </p>
          {status.configured && (
            <p className="mt-1 break-all font-mono text-micro text-faint">
              {t('config.diagTelemetryActive', {
                endpoint: status.endpoint,
                owner: status.owner || t('config.diagTelemetryApp'),
              })}
              {status.insecure && ` · ${t('config.diagTelemetryInsecure')}`}
              {status.headerNames.length > 0 &&
                ` · ${t('config.diagTelemetryHeaders', {
                  names: status.headerNames.join(', '),
                })}`}
            </p>
          )}
        </div>
        <Segmented
          value={status.enabled ? 'on' : 'off'}
          onChange={(next) => void setEnabled(next === 'on')}
          options={[
            { value: 'on', label: t('config.diagTelemetryOn') },
            { value: 'off', label: t('config.diagTelemetryOff') },
          ]}
        />
      </div>
      {error && (
        <p className="mt-1.5 text-label text-err break-words">{error}</p>
      )}
    </div>
  );
}
