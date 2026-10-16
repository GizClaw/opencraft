import { create } from 'zustand';
import { api } from '../lib/api';
import {
  activatePlugin,
  getContributions,
  resetHost,
  sortByOrder,
  startHost,
} from './host';
import type {
  CommandContribution,
  PluginSummary,
  SettingsPanelContribution,
  SidebarEntryContribution,
  StatusBarContribution,
} from './types';

interface PluginState {
  plugins: PluginSummary[];
  panels: SettingsPanelContribution[];
  entries: SidebarEntryContribution[];
  commands: CommandContribution[];
  statusBar: StatusBarContribution[];
  errors: Record<string, string>;
  loading: boolean;
  /**
   * Runs one load cycle: list the registry, rebuild the Cordis app and
   * collect what the enabled plugins contribute. A pass that is already
   * in flight is shared (see `inflight`); `force` queues a fresh pass
   * behind it, which is what a write needs.
   */
  load: (opts?: { force?: boolean }) => Promise<void>;
  setEnabled: (id: string, enabled: boolean) => Promise<void>;
}

// In-flight load: React StrictMode double-invokes effects in dev, and
// refresh/install flows can race. Concurrent load() calls must share
// one pass, otherwise resetHost() rebuilds the Cordis app twice and
// every plugin is activated twice (duplicated panels/commands).
let inflight: Promise<void> | null = null;
// Every pass gets a generation: only the newest one clears `inflight`,
// so a pass that was queued behind another does not lose its slot when
// the older one settles.
let inflightGeneration = 0;

export const usePluginStore = create<PluginState>((set, get) => {
  const runPass = async () => {
    set({ loading: true });
    try {
      const plugins = (await api.pluginList()) ?? [];
      // A fresh Cordis app per load cycle: previous plugin scopes are
      // disposed (their effects run in reverse order) and
      // contributions are collected anew.
      await resetHost();
      const panels: SettingsPanelContribution[] = [];
      const entries: SidebarEntryContribution[] = [];
      const commands: CommandContribution[] = [];
      const statusBar: StatusBarContribution[] = [];
      const errors: Record<string, string> = {};
      for (const p of plugins) {
        if (p.error) {
          errors[p.id] = p.error;
          continue;
        }
        if (!p.enabled) continue;
        try {
          const src = await api.pluginBundle(p.id);
          await activatePlugin(p.id, src, p.permissions);
        } catch (err) {
          errors[p.id] = String(err);
        }
      }
      await startHost();
      const c = getContributions();
      panels.push(...c.settingsPanels);
      entries.push(...c.sidebarEntries);
      commands.push(...c.commands);
      statusBar.push(...c.statusBar);
      panels.sort(sortByOrder);
      entries.sort(sortByOrder);
      commands.sort(sortByOrder);
      statusBar.sort(sortByOrder);
      set({ plugins, panels, entries, commands, statusBar, errors });
    } catch (err) {
      set({ errors: { _host: String(err) } });
    } finally {
      set({ loading: false });
    }
  };

  // startPass publishes one pass as the shared in-flight one and clears
  // it when it settles.
  const startPass = (pass: Promise<void>) => {
    const generation = ++inflightGeneration;
    const tracked = pass.finally(() => {
      if (inflightGeneration === generation) inflight = null;
    });
    inflight = tracked;
    return tracked;
  };

  return {
    plugins: [],
    panels: [],
    entries: [],
    commands: [],
    statusBar: [],
    errors: {},
    loading: false,

    load: (opts) => {
      if (inflight) {
        if (!opts?.force) return inflight;
        // A write just landed. The pass in flight may have read the list
        // *before* it, so publishing that result would show the user the
        // pre-write state (the toggle appears to flip back until the
        // next reload). Queue a fresh pass behind it instead of reusing
        // it, so the in-flight pass keeps its own result and the last
        // word belongs to the newer pass.
        const previous = inflight;
        return startPass(previous.then(runPass, runPass));
      }
      return startPass(runPass());
    },

    setEnabled: async (id, enabled) => {
      await api.pluginSetEnabled(id, enabled);
      await get().load({ force: true });
    },
  };
});
