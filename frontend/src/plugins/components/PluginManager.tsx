import {
  Download,
  Loader2,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Puzzle,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import { usePluginStore } from '../store';
import { compareVersions } from '../version';
import { PluginDetailDrawer } from './PluginDetailDrawer';
import { PluginInstallDialog } from './PluginInstallDialog';
import type { PluginSummary, PluginUpdateInfo } from '../types';

// PluginManager is the "插件" settings tab: a searchable plugin list
// with a "..." action menu. Clicking a plugin opens the detail drawer,
// where its agent-facing capabilities are shown inline.
export function PluginManager({ showTitle = true }: { showTitle?: boolean }) {
  const { t } = useTranslation();
  const plugins = usePluginStore((s) => s.plugins);
  const errors = usePluginStore((s) => s.errors);
  const loading = usePluginStore((s) => s.loading);
  const load = usePluginStore((s) => s.load);
  const setEnabled = usePluginStore((s) => s.setEnabled);
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
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

  useEffect(() => {
    if (!menuFor) return;
    const onDown = (e: PointerEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuFor(null);
        setConfirmRollbackId(null);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [menuFor]);

  const filtered = plugins.filter((p) => {
    const q = query.trim().toLowerCase();
    if (!q) return true;
    return (
      p.name.toLowerCase().includes(q) ||
      p.id.toLowerCase().includes(q) ||
      p.version.toLowerCase().includes(q)
    );
  });

  const selected = plugins.find((p) => p.id === selectedId) ?? null;
  useEffect(() => {
    if (selectedId && !plugins.some((p) => p.id === selectedId)) {
      setSelectedId(null);
    }
  }, [plugins, selectedId]);

  const uninstall = async (id: string) => {
    setConfirmUninstallId(null);
    try {
      await api.pluginUninstall(id);
      await load();
    } catch (err) {
      setActionErrors((prev) => ({ ...prev, [id]: String(err) }));
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
    } finally {
      setConfirmRollbackId(null);
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

  const toggleEnabled = async (p: PluginSummary) => {
    setMenuFor(null);
    try {
      await setEnabled(p.id, !p.enabled);
    } catch (err) {
      setActionErrors((prev) => ({ ...prev, [p.id]: String(err) }));
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

      <div className="relative">
        <Search
          size="0.8571rem"
          className="absolute left-2.5 top-1/2 -translate-y-1/2 text-dim"
        />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('config.pluginsSearch')}
          className="w-full rounded-lg border border-edge bg-panel pl-8 pr-3 py-1.5 text-sm outline-none focus:border-accent"
        />
      </div>

      {filtered.length === 0 ? (
        <div className="rounded-xl border border-edge bg-panel2 p-6 text-center text-sm text-dim">
          {plugins.length === 0
            ? t('config.pluginsEmpty')
            : t('config.pluginsSearchEmpty')}
        </div>
      ) : (
        <ul className="flex flex-col gap-2">
          {filtered.map((p) => (
            <li
              key={p.id}
              className="rounded-xl border border-edge bg-panel2 p-3"
            >
              <div className="flex items-center gap-2">
                <button
                  onClick={() => setSelectedId(p.id)}
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                >
                  <Puzzle size="1.0000rem" className="shrink-0 text-dim" />
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-1.5">
                      <span className="min-w-0 truncate text-sm font-semibold">
                        {p.name}
                      </span>
                      <span className="shrink-0 text-xs text-dim">
                        v{p.version}
                      </span>
                      {!p.enabled && (
                        <span className="shrink-0 rounded border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim">
                          {t('config.pluginsDisabled')}
                        </span>
                      )}
                      {p.builtin && (
                        <span
                          className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim"
                          title={t('config.pluginsBuiltinHint')}
                        >
                          {t('config.pluginsBuiltin')}
                        </span>
                      )}
                      {p.shadowsBuiltin && (
                        <span className="shrink-0 rounded bg-warn/10 px-1.5 py-0.5 text-[0.7143rem] text-warn">
                          {t('config.pluginsShadow')}
                        </span>
                      )}
                      {p.hasSkills && (
                        <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                          {t('config.pluginsCapabilitySkills')}
                        </span>
                      )}
                      {p.hasMcp && (
                        <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                          {t('config.pluginsCapabilityMcp')}
                        </span>
                      )}
                      {p.hasHooks && (
                        <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                          {t('config.pluginsCapabilityHooks')}
                        </span>
                      )}
                      {p.hasTools && (
                        <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                          {t('config.pluginsCapabilityTools')}
                        </span>
                      )}
                    </span>
                    <span className="block truncate font-mono text-xs text-dim">
                      {p.id}
                    </span>
                  </span>
                </button>

                <div className="relative shrink-0">
                  <button
                    onClick={() => setMenuFor(menuFor === p.id ? null : p.id)}
                    aria-label={t('config.pluginsMore')}
                    title={t('config.pluginsMore')}
                    className="rounded-lg p-1.5 text-dim hover:bg-panel hover:text-fg"
                  >
                    <MoreHorizontal size="1.0000rem" />
                  </button>
                  {menuFor === p.id && (
                    <div
                      ref={menuRef}
                      className="absolute right-0 top-full z-40 mt-1.5 w-48 rounded-lg border border-edge bg-panel p-1 shadow-xl"
                    >
                      <button
                        onClick={() => void toggleEnabled(p)}
                        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
                      >
                        {p.enabled ? (
                          <Pause size="0.8571rem" className="shrink-0" />
                        ) : (
                          <Play size="0.8571rem" className="shrink-0" />
                        )}
                        <span className="flex-1 text-left">
                          {p.enabled
                            ? t('config.pluginsDisable')
                            : t('config.pluginsEnable')}
                        </span>
                      </button>
                      {!p.builtin && (
                        <button
                          onClick={() => {
                            setMenuFor(null);
                            setUpdateId(p.id);
                          }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
                        >
                          <Pencil size="0.8571rem" className="shrink-0" />
                          <span className="flex-1 text-left">
                            {t('config.pluginsUpdate')}
                          </span>
                        </button>
                      )}
                      {p.hasUpdate && (
                        <button
                          onClick={() => void checkUpdate(p.id)}
                          disabled={checkingUpdateId === p.id}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg disabled:opacity-40"
                        >
                          {checkingUpdateId === p.id ? (
                            <Loader2
                              size="0.8571rem"
                              className="shrink-0 animate-spin"
                            />
                          ) : (
                            <RefreshCw size="0.8571rem" className="shrink-0" />
                          )}
                          <span className="flex-1 text-left">
                            {checkingUpdateId === p.id
                              ? t('config.pluginsCheckingUpdate')
                              : t('config.pluginsCheckUpdate')}
                          </span>
                        </button>
                      )}
                      {!p.builtin && p.canRollback && (
                        <button
                          onClick={() => {
                            if (confirmRollbackId === p.id) {
                              setMenuFor(null);
                              void rollback(p.id);
                            } else {
                              setConfirmRollbackId(p.id);
                            }
                          }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
                        >
                          <RefreshCw
                            size="0.8571rem"
                            className="shrink-0 -scale-x-100"
                          />
                          <span className="flex-1 text-left">
                            {confirmRollbackId === p.id
                              ? t('config.pluginsRollbackConfirm')
                              : t('config.pluginsRollback')}
                          </span>
                        </button>
                      )}
                      <div className="my-1 border-t border-edge" />
                      {!p.builtin && (
                        <button
                          onClick={() => {
                            setMenuFor(null);
                            setConfirmUninstallId(p.id);
                          }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-err/10 hover:text-err"
                        >
                          <Trash2 size="0.8571rem" className="shrink-0" />
                          <span className="flex-1 text-left">
                            {t('config.pluginsUninstall')}
                          </span>
                        </button>
                      )}
                    </div>
                  )}
                </div>
              </div>

              {p.shadowsBuiltin &&
                p.builtinVersion &&
                compareVersions(p.version, p.builtinVersion) < 0 && (
                  <p className="mt-1.5 text-[0.7857rem] text-warn">
                    {t('config.pluginsShadowOlder', {
                      version: p.version,
                      builtinVersion: p.builtinVersion,
                    })}
                  </p>
                )}

              {p.error && (
                <p className="mt-1.5 text-[0.7857rem] text-err break-words">
                  {p.error}
                </p>
              )}
              {errors[p.id] && (
                <p className="mt-1.5 text-[0.7857rem] text-err break-words">
                  {t('config.pluginLoadError')}: {errors[p.id]}
                </p>
              )}
              {actionErrors[p.id] && (
                <p className="mt-1.5 text-[0.7857rem] text-err break-words">
                  {actionErrors[p.id]}
                </p>
              )}

              {confirmUninstallId === p.id && (
                <div className="mt-2 flex items-center gap-2 rounded-lg border border-err/40 bg-err/10 px-2 py-1.5 text-xs text-dim">
                  <span className="min-w-0 flex-1 truncate">
                    {t('config.pluginsUninstallConfirm')}
                  </span>
                  <button
                    onClick={() => void uninstall(p.id)}
                    className="rounded bg-err px-2 py-1 text-white"
                  >
                    {t('config.pluginsUninstall')}
                  </button>
                  <button
                    onClick={() => setConfirmUninstallId(null)}
                    className="rounded border border-edge px-2 py-1"
                  >
                    {t('interact.cancel')}
                  </button>
                </div>
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
      )}

      {selected && (
        <PluginDetailDrawer
          plugin={selected}
          onClose={() => setSelectedId(null)}
        />
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
