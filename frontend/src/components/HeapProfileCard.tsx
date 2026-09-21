import { HardDriveDownload } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { ICON } from './ui/icon';

// HeapProfileCard captures a runtime/pprof heap profile on demand. A long
// session's Go heap is what these diagnostics exist for, and the profile
// is only useful when taken while the process is big — so the card hands
// back the file path and the pprof command instead of pretending the
// number in the app is enough.
// showTitle is off when the card sits under a section heading that
// already carries the same name.
export function HeapProfileCard({ showTitle = true }: { showTitle?: boolean }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ path: string; bytes: number } | null>(
    null,
  );
  const [error, setError] = useState('');

  const capture = async () => {
    setBusy(true);
    setError('');
    try {
      const captured = await api.captureHeapProfile();
      setResult(captured);
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="rounded-card border border-edge bg-panel2 p-3">
      <div className="flex items-center gap-2">
        <HardDriveDownload size={ICON.sm} className="shrink-0 text-accent" />
        {showTitle && (
          <span className="text-sm font-medium text-fg">
            {t('config.heapProfileTitle')}
          </span>
        )}
        <span className="flex-1" />
        <button
          type="button"
          onClick={() => void capture()}
          disabled={busy}
          className="rounded-control border border-edge px-3 py-1 text-xs text-fg hover:border-accent/50 disabled:opacity-50"
        >
          {busy
            ? t('config.heapProfileCapturing')
            : t('config.heapProfileCapture')}
        </button>
      </div>
      <p className="mt-1.5 text-xs text-dim">{t('config.heapProfileHint')}</p>
      {result && (
        <p className="mt-1.5 break-all font-mono text-xs text-ok">
          {t('config.heapProfileSaved', {
            path: result.path,
            size: Math.max(1, Math.round(result.bytes / 1024)),
          })}
        </p>
      )}
      {error && <p className="mt-1.5 text-xs text-err">{error}</p>}
    </div>
  );
}
