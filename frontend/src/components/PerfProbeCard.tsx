import { Activity } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { setPerfProbeEnabled } from '../lib/perfProbe';
import { ICON } from './ui/icon';
import { Segmented } from './ui/Segmented';

// PerfProbeCard is the diagnostics switch for the renderer-side sampler.
// The Go heap says nothing about the page, and the renderer is where a
// long conversation actually hurts — so this is what turns "the app got
// slow" into a series: DOM nodes, frame gaps, stream-flush timings and
// the number of loaded transcript rows, every 30s, logged as
// `frontend rum:` lines and recorded as frontend.* metrics.
// showTitle is off when the card sits under a section heading that
// already carries the same name.
export function PerfProbeCard({ showTitle = true }: { showTitle?: boolean }) {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    void (async () => {
      try {
        setEnabled(await api.perfProbe());
      } catch (err) {
        setError(String(err));
      }
    })();
  }, []);

  const toggle = async (next: boolean) => {
    const previous = enabled;
    setEnabled(next);
    setError('');
    try {
      await api.setPerfProbe(next);
      setPerfProbeEnabled(next);
    } catch (err) {
      setEnabled(previous ?? null);
      setError(String(err));
    }
  };

  if (enabled === null) {
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
              <Activity size={ICON.sm} className="text-accent" />
              {t('config.diagPerfProbeTitle')}
            </div>
          )}
          <p className="mt-1 text-xs text-dim">
            {t('config.diagPerfProbeHint')}
          </p>
          <p className="mt-1 text-micro text-faint">
            {t('config.diagPerfProbeSeries')}
          </p>
        </div>
        <Segmented
          value={enabled ? 'on' : 'off'}
          onChange={(next) => void toggle(next === 'on')}
          options={[
            { value: 'on', label: t('config.diagPerfProbeOn') },
            { value: 'off', label: t('config.diagPerfProbeOff') },
          ]}
        />
      </div>
      {error && (
        <p className="mt-1.5 text-label text-err break-words">{error}</p>
      )}
    </div>
  );
}
