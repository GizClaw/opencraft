import { Download, Puzzle, RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import { usePluginStore } from '../store';
import { PluginInstallDialog } from './PluginInstallDialog';

// PluginManager is the "插件" settings tab: installed plugins with
// enable/disable toggles, per-plugin load errors, and the panels each
// enabled plugin contributes (settingsPanels contribution point).
export function PluginManager() {
  const { t } = useTranslation();
  const plugins = usePluginStore((s) => s.plugins);
  const panels = usePluginStore((s) => s.panels);
  const errors = usePluginStore((s) => s.errors);
  const loading = usePluginStore((s) => s.loading);
  const load = usePluginStore((s) => s.load);
  const setEnabled = usePluginStore((s) => s.setEnabled);
  const [installOpen, setInstallOpen] = useState(false);
  const [confirmUninstallId, setConfirmUninstallId] = useState<string | null>(
    null,
  );

  const uninstall = async (id: string) => {
    setConfirmUninstallId(null);
    try {
      await api.pluginUninstall(id);
      await load();
    } catch (err) {
      console.error('plugin uninstall failed', err);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">{t('config.tabPlugins')}</h2>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void load()}
            className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1 text-xs text-dim hover:text-fg"
          >
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
            {t('config.pluginsRefresh')}
          </button>
          <button
            onClick={() => setInstallOpen(true)}
            className="flex items-center gap-1.5 rounded-lg bg-accent px-2.5 py-1 text-xs text-white hover:opacity-90"
          >
            <Download size={12} />
            {t('config.pluginsInstall')}
          </button>
        </div>
      </div>

      {errors._host && (
        <p className="text-xs text-err">{errors._host}</p>
      )}

      {plugins.length === 0 && !errors._host && (
        <p className="text-xs text-dim">{t('config.pluginsEmpty')}</p>
      )}

      <ul className="flex flex-col gap-2">
        {plugins.map((p) => (
          <li
            key={p.id}
            className="rounded-xl border border-edge bg-panel2 p-3"
          >
            <div className="flex items-center gap-2">
              <Puzzle size={14} className="text-dim shrink-0" />
              <div className="min-w-0 flex-1">
                <p className="text-sm text-fg truncate">
                  {p.name}{' '}
                  <span className="text-xs text-dim">v{p.version}</span>
                </p>
                <p className="text-[11px] text-dim truncate">{p.id}</p>
              </div>
              <button
                onClick={() => void setEnabled(p.id, !p.enabled)}
                className={`rounded-md px-2 py-1 text-xs ${
                  p.enabled
                    ? 'bg-accent text-white hover:opacity-90'
                    : 'border border-edge text-dim hover:text-fg'
                }`}
              >
                {p.enabled
                  ? t('config.pluginsDisable')
                  : t('config.pluginsEnable')}
              </button>
              <button
                onClick={() => {
                  if (confirmUninstallId === p.id) {
                    void uninstall(p.id);
                  } else {
                    setConfirmUninstallId(p.id);
                  }
                }}
                className="rounded-md px-2 py-1 text-xs text-dim hover:text-err"
              >
                {confirmUninstallId === p.id
                  ? t('config.pluginsUninstallConfirm')
                  : t('config.pluginsUninstall')}
              </button>
            </div>
            {p.error && (
              <p className="mt-2 text-[11px] text-err break-words">
                {p.error}
              </p>
            )}
            {errors[p.id] && (
              <p className="mt-2 text-[11px] text-err break-words">
                {t('config.pluginLoadError')}: {errors[p.id]}
              </p>
            )}
          </li>
        ))}
      </ul>

      {panels.length > 0 && (
        <div className="flex flex-col gap-3">
          {panels.map((panel) => (
            <section
              key={panel.id}
              className="rounded-xl border border-edge bg-panel2 p-3"
            >
              <h3 className="mb-2 text-xs font-semibold text-dim">
                {panel.title}
              </h3>
              <panel.Component />
            </section>
          ))}
        </div>
      )}

      {installOpen && (
        <PluginInstallDialog onClose={() => setInstallOpen(false)} />
      )}
    </div>
  );
}
