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
  load: () => Promise<void>;
  setEnabled: (id: string, enabled: boolean) => Promise<void>;
}

// In-flight load: React StrictMode double-invokes effects in dev, and
// refresh/install flows can race. Concurrent load() calls must share
// one pass, otherwise resetHost() rebuilds the Cordis app twice and
// every plugin is activated twice (duplicated panels/commands).
let inflight: Promise<void> | null = null;

export const usePluginStore = create<PluginState>((set, get) => ({
  plugins: [],
  panels: [],
  entries: [],
  commands: [],
  statusBar: [],
  errors: {},
  loading: false,

  load: () => {
    if (inflight) return inflight;
    inflight = (async () => {
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
    })().finally(() => {
      inflight = null;
    });
    return inflight;
  },

  setEnabled: async (id, enabled) => {
    await api.pluginSetEnabled(id, enabled);
    await get().load();
  },
}));
