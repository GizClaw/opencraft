import { FileArchive, FolderOpen, Loader2, X } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import { usePluginStore } from '../store';

// PluginInstallDialog installs a plugin from a local directory
// containing plugin.json or from a zip package (release artifact).
export function PluginInstallDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const load = usePluginStore((s) => s.load);
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const pick = async () => {
    try {
      const dir = await api.pickFolder(t('config.pluginsInstallTitle'));
      if (dir) {
        setPath(dir);
        setError('');
      }
    } catch (err) {
      setError(String(err));
    }
  };

  const pickZip = async () => {
    try {
      const file = await api.pickFile(t('config.pluginsInstallTitle'), '*.zip');
      if (file) {
        setPath(file);
        setError('');
      }
    } catch (err) {
      setError(String(err));
    }
  };

  const install = async () => {
    const p = path.trim();
    if (!p || busy) return;
    setBusy(true);
    setError('');
    try {
      if (p.toLowerCase().endsWith('.zip')) {
        await api.pluginInstallZip(p);
      } else {
        await api.pluginInstall(p);
      }
      await load();
      onClose();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        className="w-[460px] rounded-2xl border border-edge bg-panel shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-edge px-4 py-3">
          <h3 className="text-sm font-semibold">
            {t('config.pluginsInstall')}
          </h3>
          <button onClick={onClose} className="text-dim hover:text-fg">
            <X size={16} />
          </button>
        </div>
        <div className="flex flex-col gap-3 p-4">
          <p className="text-xs text-dim">{t('config.pluginsInstallHint')}</p>
          <div className="flex gap-2">
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void install();
              }}
              placeholder={t('config.pluginsInstallPathPlaceholder')}
              className="min-w-0 flex-1 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-fg outline-none focus:border-accent"
              autoFocus
            />
            <button
              onClick={() => void pick()}
              className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-dim hover:text-fg"
            >
              <FolderOpen size={12} />
              {t('config.pluginsChooseFolder')}
            </button>
            <button
              onClick={() => void pickZip()}
              className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-dim hover:text-fg"
            >
              <FileArchive size={12} />
              {t('config.pluginsChooseZip')}
            </button>
          </div>
          {error && <p className="text-[11px] text-err break-words">{error}</p>}
          <div className="flex justify-end gap-2">
            <button
              onClick={onClose}
              className="rounded-lg px-3 py-1.5 text-xs text-dim hover:text-fg"
            >
              {t('config.cancel')}
            </button>
            <button
              onClick={() => void install()}
              disabled={busy || !path.trim()}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs text-white hover:opacity-90 disabled:opacity-50"
            >
              {busy && <Loader2 size={12} className="animate-spin" />}
              {t('config.pluginsInstall')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
