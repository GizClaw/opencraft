import { Puzzle, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { compareVersions } from '../version';
import { usePluginStore } from '../store';
import type { PluginSummary } from '../types';
import { PluginCapabilitiesSection } from './PluginCapabilities';
import { PluginPanels } from './PluginPanels';

// PluginDetailDrawer is the right-side plugin detail page. It shows the
// plugin's metadata and embeds the agent-facing capabilities inline.
export function PluginDetailDrawer({
  plugin,
  onClose,
}: {
  plugin: PluginSummary;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const commands = usePluginStore((s) => s.commands);
  const pluginCommands = commands.filter((c) => c.pluginId === plugin.id);

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/30" onClick={onClose} />
      <aside
        className="fixed inset-y-0 right-0 z-50 flex w-[30rem] max-w-[94vw] flex-col border-l border-edge bg-panel shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-label={plugin.name}
      >
        <div className="flex shrink-0 items-center justify-between border-b border-edge px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <Puzzle size="1.0714rem" className="shrink-0 text-dim" />
            <h3 className="min-w-0 truncate text-sm font-semibold">
              {plugin.name}
            </h3>
            <span className="shrink-0 text-xs text-dim">v{plugin.version}</span>
          </div>
          <button
            onClick={onClose}
            aria-label={t('tools.close')}
            className="text-dim hover:text-fg"
          >
            <X size="1.1428rem" />
          </button>
        </div>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          <div className="space-y-1.5">
            <p className="break-all font-mono text-xs text-dim">{plugin.id}</p>
          </div>

          {plugin.shadowsBuiltin &&
            plugin.builtinVersion &&
            compareVersions(plugin.version, plugin.builtinVersion) < 0 && (
              <p className="text-[0.7857rem] text-warn">
                {t('config.pluginsShadowOlder', {
                  version: plugin.version,
                  builtinVersion: plugin.builtinVersion,
                })}
              </p>
            )}

          {plugin.error && (
            <p className="rounded-lg border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
              {plugin.error}
            </p>
          )}

          {(plugin.hasHooks || plugin.hasTools) && (
            <p className="text-xs text-warn">
              {t('config.pluginsCapabilitiesWarning')}
            </p>
          )}

          <PluginPanels tab="plugins" pluginId={plugin.id} />

          {pluginCommands.length > 0 && (
            <section>
              <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">
                {t('config.pluginsCommands')}
              </h4>
              <div className="flex flex-wrap gap-2">
                {pluginCommands.map((cmd) => (
                  <button
                    key={cmd.id}
                    onClick={() => cmd.run()}
                    className="rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs hover:border-accent/50"
                  >
                    {cmd.title}
                  </button>
                ))}
              </div>
            </section>
          )}

          <section>
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">
              {t('config.pluginsCapabilities')}
            </h4>
            {plugin.hasTools || plugin.hasSkills ? (
              <PluginCapabilitiesSection
                pluginId={plugin.id}
                hasTools={!!plugin.hasTools}
                hasSkills={!!plugin.hasSkills}
              />
            ) : (
              <p className="text-xs text-dim">
                {t('config.pluginsNoCapabilities')}
              </p>
            )}
          </section>
        </div>
      </aside>
    </>
  );
}
