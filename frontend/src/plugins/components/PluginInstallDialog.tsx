import { FileArchive, FolderOpen, Loader2, X } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import { usePluginStore } from '../store';
import { compareVersions } from '../version';
import type { PluginSummary } from '../types';

// PluginInstallDialog installs or updates a plugin from a local
// directory containing plugin.json or from a zip package. When
// pluginId is set it runs the update flow instead of install.
export function PluginInstallDialog({
  onClose,
  pluginId,
}: {
  onClose: () => void;
  pluginId?: string;
}) {
  const { t } = useTranslation();
  const load = usePluginStore((s) => s.load);
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [shadow, setShadow] = useState<PluginSummary | null>(null);
  const isUpdate = pluginId !== undefined;

  const inspect = async (p: string) => {
    if (!p.trim() || isUpdate) {
      setShadow(null);
      return;
    }
    try {
      setShadow(await api.pluginInspect(p.trim()));
    } catch {
      setShadow(null);
    }
  };

  const pick = async () => {
    try {
      const dir = await api.pickFolder(
        t(
          isUpdate ? 'config.pluginsUpdateTitle' : 'config.pluginsInstallTitle',
        ),
      );
      if (dir) {
        setPath(dir);
        setError('');
        void inspect(dir);
      }
    } catch (err) {
      setError(String(err));
    }
  };

  const pickZip = async () => {
    try {
      const file = await api.pickFile(
        t(
          isUpdate ? 'config.pluginsUpdateTitle' : 'config.pluginsInstallTitle',
        ),
        '*.zip',
      );
      if (file) {
        setPath(file);
        setError('');
        void inspect(file);
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
        if (isUpdate) {
          await api.pluginUpdateZip(pluginId, p);
        } else {
          await api.pluginInstallZip(p);
        }
      } else {
        if (isUpdate) {
          await api.pluginUpdate(pluginId, p);
        } else {
          await api.pluginInstall(p);
        }
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
        className="w-[32.8571rem] rounded-2xl border border-edge bg-panel shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-edge px-4 py-3">
          <h3 className="text-sm font-semibold">
            {t(isUpdate ? 'config.pluginsUpdate' : 'config.pluginsInstall')}
          </h3>
          <button onClick={onClose} className="text-dim hover:text-fg">
            <X size="1.1429rem" />
          </button>
        </div>
        <div className="flex flex-col gap-3 p-4">
          <p className="text-xs text-dim">
            {t(
              isUpdate
                ? 'config.pluginsUpdateHint'
                : 'config.pluginsInstallHint',
            )}
          </p>
          <div className="flex gap-2">
            <input
              value={path}
              onChange={(e) => {
                setPath(e.target.value);
                void inspect(e.target.value);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void install();
              }}
              placeholder={t(
                isUpdate
                  ? 'config.pluginsUpdatePathPlaceholder'
                  : 'config.pluginsInstallPathPlaceholder',
              )}
              className="min-w-0 flex-1 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-fg outline-none focus:border-accent"
              autoFocus
            />
            <button
              onClick={() => void pick()}
              className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-dim hover:text-fg"
            >
              <FolderOpen size="0.8571rem" />
              {t('config.pluginsChooseFolder')}
            </button>
            <button
              onClick={() => void pickZip()}
              className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-dim hover:text-fg"
            >
              <FileArchive size="0.8571rem" />
              {t('config.pluginsChooseZip')}
            </button>
          </div>
          {shadow?.shadowsBuiltin && (
            <div className="rounded-lg border border-warn/30 bg-warn/10 px-2.5 py-2 text-xs text-warn">
              {t('config.pluginsShadowInstallWarn', {
                name: shadow.name,
                builtinVersion: shadow.builtinVersion ?? '',
              })}
              {shadow.builtinVersion &&
                compareVersions(shadow.version, shadow.builtinVersion) < 0 && (
                  <p className="mt-1">
                    {t('config.pluginsShadowInstallTooOld', {
                      version: shadow.version,
                      builtinVersion: shadow.builtinVersion,
                    })}
                  </p>
                )}
            </div>
          )}
          {error && (
            <p className="text-[0.7857rem] text-err break-words">{error}</p>
          )}
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
              {busy && <Loader2 size="0.8571rem" className="animate-spin" />}
              {t(isUpdate ? 'config.pluginsUpdate' : 'config.pluginsInstall')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
