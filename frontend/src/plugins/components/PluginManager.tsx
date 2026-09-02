import {
  BookOpen,
  ChevronDown,
  ChevronRight,
  Download,
  Puzzle,
  RefreshCw,
  Wrench,
} from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import { usePluginStore } from '../store';
import { compareVersions } from '../version';
import { PluginInstallDialog } from './PluginInstallDialog';
import { PluginPanels } from './PluginPanels';
import type { PluginSkillDTO, PluginToolDTO, PluginUpdateInfo } from '../types';

// PluginCapabilitiesList lazily loads one plugin's agent-facing tools
// and skills and renders them in a single expandable panel. Tools are
// only invokable by the agent through the tool catalog; skills come
// from the shared skills registry. This is the plugin manager's
// visibility surface for both.
function PluginCapabilitiesList({
  pluginId,
  hasTools,
  hasSkills,
}: {
  pluginId: string;
  hasTools: boolean;
  hasSkills: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [tools, setTools] = useState<PluginToolDTO[] | null>(null);
  const [skills, setSkills] = useState<PluginSkillDTO[] | null>(null);
  const [error, setError] = useState('');

  const toggle = async () => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (tools === null || skills === null) {
      try {
        const [toolRes, skillRes] = await Promise.all([
          hasTools ? api.pluginTools(pluginId) : Promise.resolve([]),
          hasSkills ? api.pluginSkills(pluginId) : Promise.resolve([]),
        ]);
        setTools(toolRes);
        setSkills(skillRes);
      } catch (err) {
        setError(String(err));
      }
    }
  };

  const loaded = tools !== null && skills !== null;

  return (
    <div className="mt-2 overflow-hidden rounded-lg border border-edge bg-panel/60">
      <button
        onClick={() => void toggle()}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left hover:bg-panel2"
      >
        <span className="flex min-w-0 items-center gap-1.5 text-xs font-medium text-fg">
          {open ? (
            <ChevronDown size="0.875rem" className="shrink-0 text-dim" />
          ) : (
            <ChevronRight size="0.875rem" className="shrink-0 text-dim" />
          )}
          <span className="truncate">{t('config.pluginsCapabilities')}</span>
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.65rem] text-dim">
            {loaded
              ? `${tools!.length} ${t('config.pluginsCapabilitiesTools')} · ${
                  skills!.length
                } ${t('config.pluginsCapabilitiesSkills')}`
              : t('config.pluginsCapabilitiesLoading')}
          </span>
        </span>
        <span className="shrink-0 text-xs text-dim">
          {open
            ? t('config.pluginsCapabilitiesHide')
            : t('config.pluginsCapabilitiesShow')}
        </span>
      </button>
      {open && (
        <div className="space-y-3 border-t border-edge px-3 py-2.5">
          {error && <p className="text-xs text-err">{error}</p>}
          {!loaded && (
            <p className="text-xs text-dim">
              {t('config.pluginsCapabilitiesLoading')}
            </p>
          )}
          {loaded && hasTools && (
            <section>
              <h4 className="mb-1.5 flex items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-wide text-dim">
                <Wrench size="0.75rem" />
                {t('config.pluginsCapabilitiesTools')}
              </h4>
              {tools!.length === 0 ? (
                <p className="text-xs text-dim">
                  {t('config.pluginsToolsEmpty')}
                </p>
              ) : (
                <div className="space-y-1.5">
                  {tools!.map((tool) => (
                    <div
                      key={tool.name}
                      className="rounded-md border border-edge bg-panel px-2.5 py-2"
                    >
                      <div className="flex items-center gap-2">
                        <span className="min-w-0 truncate font-mono text-xs font-medium text-fg">
                          {tool.name}
                        </span>
                        {tool.mutates_state && (
                          <span className="shrink-0 rounded bg-warn/10 px-1.5 py-0.5 text-[0.65rem] text-warn">
                            {t('config.pluginsToolMutates')}
                          </span>
                        )}
                      </div>
                      {tool.description && (
                        <p className="mt-1 text-xs leading-relaxed text-dim">
                          {tool.description}
                        </p>
                      )}
                      <p className="mt-1 font-mono text-[0.7rem] text-dim/70">
                        {tool.method}
                      </p>
                    </div>
                  ))}
                </div>
              )}
            </section>
          )}
          {loaded && hasSkills && (
            <section>
              <h4 className="mb-1.5 flex items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-wide text-dim">
                <BookOpen size="0.75rem" />
                {t('config.pluginsCapabilitiesSkills')}
              </h4>
              {skills!.length === 0 ? (
                <p className="text-xs text-dim">
                  {t('config.pluginsSkillsEmpty')}
                </p>
              ) : (
                <div className="space-y-1.5">
                  {skills!.map((skill) => (
                    <div
                      key={skill.path}
                      className="rounded-md border border-edge bg-panel px-2.5 py-2"
                    >
                      <div className="flex items-center gap-2">
                        <span className="min-w-0 truncate font-mono text-xs font-medium text-fg">
                          {skill.name}
                        </span>
                        {skill.scope && (
                          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.65rem] text-dim">
                            {skill.scope}
                          </span>
                        )}
                      </div>
                      {skill.description && (
                        <p className="mt-1 text-xs leading-relaxed text-dim">
                          {skill.description}
                        </p>
                      )}
                      <p className="mt-1 break-all font-mono text-[0.7rem] text-dim/60">
                        {skill.path}
                      </p>
                    </div>
                  ))}
                </div>
              )}
            </section>
          )}
        </div>
      )}
    </div>
  );
}

// PluginManager is the "插件" settings tab: installed plugins with
// enable/disable toggles, per-plugin load errors, and the panels each
// enabled plugin contributes (settingsPanels contribution point).
export function PluginManager({ showTitle = true }: { showTitle?: boolean }) {
  const { t } = useTranslation();
  const plugins = usePluginStore((s) => s.plugins);
  const commands = usePluginStore((s) => s.commands);
  const errors = usePluginStore((s) => s.errors);
  const loading = usePluginStore((s) => s.loading);
  const load = usePluginStore((s) => s.load);
  const setEnabled = usePluginStore((s) => s.setEnabled);
  const [installOpen, setInstallOpen] = useState(false);
  const [updateId, setUpdateId] = useState<string | null>(null);
  const [confirmUninstallId, setConfirmUninstallId] = useState<string | null>(
    null,
  );
  const [confirmRollbackId, setConfirmRollbackId] = useState<string | null>(
    null,
  );
  const [actionErrors, setActionErrors] = useState<Record<string, string>>({});
  const [updateInfo, setUpdateInfo] = useState<
    Record<string, PluginUpdateInfo>
  >({});
  const [checkingUpdateId, setCheckingUpdateId] = useState<string | null>(null);
  const [applyingUpdateId, setApplyingUpdateId] = useState<string | null>(null);

  const uninstall = async (id: string) => {
    setConfirmUninstallId(null);
    try {
      await api.pluginUninstall(id);
      await load();
    } catch (err) {
      console.error('plugin uninstall failed', err);
    }
  };

  const rollback = async (id: string) => {
    try {
      await api.pluginRollback(id);
      setActionErrors((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      await load();
    } catch (err) {
      setActionErrors((prev) => ({ ...prev, [id]: String(err) }));
    }
  };

  const checkUpdate = async (id: string) => {
    setCheckingUpdateId(id);
    setActionErrors((prev) => {
      const next = { ...prev };
      delete next[id];
      return next;
    });
    try {
      const info = await api.pluginCheckUpdate(id);
      setUpdateInfo((prev) => ({ ...prev, [id]: info }));
    } catch (err) {
      setActionErrors((prev) => ({ ...prev, [id]: String(err) }));
    } finally {
      setCheckingUpdateId(null);
    }
  };

  const applyUpdate = async (id: string) => {
    setApplyingUpdateId(id);
    try {
      await api.pluginApplyUpdate(id);
      setUpdateInfo((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      await load();
    } catch (err) {
      setActionErrors((prev) => ({ ...prev, [id]: String(err) }));
    } finally {
      setApplyingUpdateId(null);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div
        className={`flex items-center ${
          showTitle ? 'justify-between' : 'justify-end'
        }`}
      >
        {showTitle && (
          <h2 className="text-sm font-semibold">{t('config.tabPlugins')}</h2>
        )}
        <div className="flex items-center gap-2">
          <button
            onClick={() => void load()}
            className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1 text-xs text-dim hover:text-fg"
          >
            <RefreshCw
              size="0.8571rem"
              className={loading ? 'animate-spin' : ''}
            />
            {t('config.pluginsRefresh')}
          </button>
          <button
            onClick={() => setInstallOpen(true)}
            className="flex items-center gap-1.5 rounded-lg bg-accent px-2.5 py-1 text-xs text-white hover:opacity-90"
          >
            <Download size="0.8571rem" />
            {t('config.pluginsInstall')}
          </button>
        </div>
      </div>

      {errors._host && <p className="text-xs text-err">{errors._host}</p>}

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
              <Puzzle size="1.0000rem" className="text-dim shrink-0" />
              <div className="min-w-0 flex-1">
                <p className="text-sm text-fg truncate">
                  {p.name}{' '}
                  <span className="text-xs text-dim">v{p.version}</span>
                  {p.builtin && (
                    <span
                      className="ml-1 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim"
                      title={t('config.pluginsBuiltinHint')}
                    >
                      {t('config.pluginsBuiltin')}
                    </span>
                  )}
                  {p.shadowsBuiltin && (
                    <span
                      className="ml-1 rounded bg-warn/10 px-1.5 py-0.5 text-[0.7143rem] text-warn"
                      title={t('config.pluginsShadowHint', {
                        version: p.builtinVersion ?? '',
                      })}
                    >
                      {t('config.pluginsShadow')}
                    </span>
                  )}
                </p>
                <p className="text-[0.7857rem] text-dim truncate">{p.id}</p>
                {p.shadowsBuiltin &&
                  p.builtinVersion &&
                  compareVersions(p.version, p.builtinVersion) < 0 && (
                    <p className="mt-1 text-[0.7857rem] text-warn">
                      {t('config.pluginsShadowOlder', {
                        version: p.version,
                        builtinVersion: p.builtinVersion,
                      })}
                    </p>
                  )}
                {(p.hasSkills || p.hasMcp || p.hasHooks || p.hasTools) && (
                  <div
                    className="mt-1 flex flex-wrap gap-1"
                    title={t('config.pluginsCapabilitiesHint')}
                  >
                    {p.hasSkills && (
                      <span className="rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                        {t('config.pluginsCapabilitySkills')}
                      </span>
                    )}
                    {p.hasMcp && (
                      <span className="rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                        {t('config.pluginsCapabilityMcp')}
                      </span>
                    )}
                    {p.hasHooks && (
                      <span className="rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                        {t('config.pluginsCapabilityHooks')}
                      </span>
                    )}
                    {p.hasTools && (
                      <span className="rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                        {t('config.pluginsCapabilityTools')}
                      </span>
                    )}
                  </div>
                )}
                {(p.hasHooks || p.hasTools) && (
                  <p className="mt-1.5 text-[0.7857rem] text-warn">
                    {t('config.pluginsCapabilitiesWarning')}
                  </p>
                )}
                {(p.hasTools || p.hasSkills) && (
                  <PluginCapabilitiesList
                    pluginId={p.id}
                    hasTools={!!p.hasTools}
                    hasSkills={!!p.hasSkills}
                  />
                )}
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
              {!p.builtin && (
                <button
                  onClick={() => setUpdateId(p.id)}
                  className="rounded-md px-2 py-1 text-xs text-dim hover:text-fg"
                >
                  {t('config.pluginsUpdate')}
                </button>
              )}
              {p.hasUpdate && (
                <button
                  onClick={() => void checkUpdate(p.id)}
                  disabled={checkingUpdateId === p.id}
                  className="rounded-md px-2 py-1 text-xs text-dim hover:text-fg disabled:opacity-50"
                >
                  {checkingUpdateId === p.id
                    ? t('config.pluginsCheckingUpdate')
                    : t('config.pluginsCheckUpdate')}
                </button>
              )}
              {!p.builtin && p.canRollback && (
                <button
                  onClick={() => {
                    if (confirmRollbackId === p.id) {
                      setConfirmRollbackId(null);
                      void rollback(p.id);
                    } else {
                      setConfirmRollbackId(p.id);
                    }
                  }}
                  className="rounded-md px-2 py-1 text-xs text-dim hover:text-warn"
                >
                  {confirmRollbackId === p.id
                    ? t('config.pluginsRollbackConfirm')
                    : t('config.pluginsRollback')}
                </button>
              )}
              {!p.builtin && (
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
              )}
            </div>
            {p.error && (
              <p className="mt-2 text-[0.7857rem] text-err break-words">
                {p.error}
              </p>
            )}
            {errors[p.id] && (
              <p className="mt-2 text-[0.7857rem] text-err break-words">
                {t('config.pluginLoadError')}: {errors[p.id]}
              </p>
            )}
            {actionErrors[p.id] && (
              <p className="mt-2 text-[0.7857rem] text-err break-words">
                {actionErrors[p.id]}
              </p>
            )}
            {updateInfo[p.id] && (
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <span className="text-[0.7857rem] text-warn">
                  {t('config.pluginsUpdateAvailable', {
                    version: updateInfo[p.id].version,
                  })}
                </span>
                <button
                  onClick={() => void applyUpdate(p.id)}
                  disabled={applyingUpdateId === p.id}
                  className="rounded-md bg-accent px-2 py-0.5 text-xs text-white hover:opacity-90 disabled:opacity-50"
                >
                  {applyingUpdateId === p.id
                    ? t('config.pluginsApplyingUpdate')
                    : t('config.pluginsApplyUpdate')}
                </button>
                {updateInfo[p.id].changelog && (
                  <p className="w-full text-[0.7857rem] text-dim break-words">
                    {updateInfo[p.id].changelog}
                  </p>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>

      <PluginPanels tab="plugins" />

      {commands.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-xs font-semibold text-dim">
            {t('config.pluginsCommands')}
          </h3>
          <div className="flex flex-wrap gap-2">
            {commands.map((cmd) => (
              <button
                key={cmd.id}
                onClick={() => cmd.run()}
                className="rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs hover:border-accent/50"
              >
                {cmd.title}
              </button>
            ))}
          </div>
        </div>
      )}

      {installOpen && (
        <PluginInstallDialog onClose={() => setInstallOpen(false)} />
      )}
      {updateId !== null && (
        <PluginInstallDialog
          pluginId={updateId}
          onClose={() => setUpdateId(null)}
        />
      )}
    </div>
  );
}
