import { Puzzle, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { compareVersions } from '../version';
import { usePluginStore } from '../store';
import type { PluginSummary } from '../types';
import { PluginCapabilitiesSection } from './PluginCapabilities';
import { PluginPanels } from './PluginPanels';
import { ICON } from '../../components/ui/icon';
import { Overlay } from '../../components/ui/Overlay';

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
      <Overlay
        open
        onClose={onClose}
        variant="drawer-right"
        ariaLabel={plugin.name}
        panelClassName="flex w-[42rem] max-w-[94vw] flex-col border-l border-edge bg-panel shadow-modal"
      >
        <div className="flex shrink-0 items-center justify-between border-b border-edge px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <Puzzle size={ICON.md} className="shrink-0 text-dim" />
            <h3 className="min-w-0 truncate text-title font-semibold">
              {plugin.name}
            </h3>
            <span className="shrink-0 text-xs text-dim">v{plugin.version}</span>
          </div>
          <button
            onClick={onClose}
            aria-label={t('tools.close')}
            className="text-dim hover:text-fg"
          >
            <X size={ICON.md} />
          </button>
        </div>

        <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4">
          <div className="space-y-1.5">
            <p className="break-all font-mono text-xs text-dim">{plugin.id}</p>
          </div>

          {plugin.shadowsBuiltin &&
            plugin.builtinVersion &&
            compareVersions(plugin.version, plugin.builtinVersion) < 0 && (
              <p className="break-words text-label text-warn">
                {t('config.pluginsShadowOlder', {
                  version: plugin.version,
                  builtinVersion: plugin.builtinVersion,
                })}
              </p>
            )}

          {plugin.error && (
            <p className="rounded-control border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
              {plugin.error}
            </p>
          )}

          {(plugin.hasHooks || plugin.hasTools) && (
            <p className="break-words text-xs text-warn">
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
                    className="max-w-full break-words rounded-control border border-edge bg-panel2 px-2.5 py-1.5 text-left text-xs hover:border-accent/50"
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

          {/* Declared manifest permissions are the plugin's host
              capabilities (secrets, session import, OTLP export, ...).
              The host enforces them; this section makes them visible. */}
          <section>
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">
              {t('config.pluginsPermissions')}
            </h4>
            {plugin.permissions.length > 0 ? (
              <ul className="flex flex-wrap gap-1.5">
                {plugin.permissions.map((perm) => (
                  <li
                    key={perm}
                    className="rounded-tight border border-edge bg-panel2 px-1.5 py-0.5 font-mono text-micro text-dim"
                  >
                    {perm}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-xs text-dim">
                {t('config.pluginsPermissionsEmpty')}
              </p>
            )}
          </section>
        </div>
      </Overlay>
    </>
  );
}
