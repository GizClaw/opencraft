import { create } from 'zustand';
import { api } from '../lib/api';
import { evaluatePlugin, sortByOrder } from './host';
import type {
  PluginContributions,
  PluginSummary,
  SettingsPanelContribution,
  SidebarEntryContribution,
} from './types';

interface PluginState {
  plugins: PluginSummary[];
  panels: SettingsPanelContribution[];
  entries: SidebarEntryContribution[];
  errors: Record<string, string>;
  loading: boolean;
  load: () => Promise<void>;
  setEnabled: (id: string, enabled: boolean) => Promise<void>;
}

export const usePluginStore = create<PluginState>((set, get) => ({
  plugins: [],
  panels: [],
  entries: [],
  errors: {},
  loading: false,

  load: async () => {
    set({ loading: true });
    try {
      const plugins = (await api.pluginList()) ?? [];
      const panels: SettingsPanelContribution[] = [];
      const entries: SidebarEntryContribution[] = [];
      const errors: Record<string, string> = {};
      for (const p of plugins) {
        if (p.error) {
          errors[p.id] = p.error;
          continue;
        }
        if (!p.enabled) continue;
        try {
          const src = await api.pluginBundle(p.id);
          const c = evaluatePlugin(p.id, src);
          panels.push(...(c.settingsPanels ?? []));
          entries.push(...(c.sidebarEntries ?? []));
        } catch (err) {
          errors[p.id] = String(err);
        }
      }
      panels.sort(sortByOrder);
      entries.sort(sortByOrder);
      set({ plugins, panels, entries, errors });
    } catch (err) {
      set({ errors: { _host: String(err) } });
    } finally {
      set({ loading: false });
    }
  },

  setEnabled: async (id, enabled) => {
    await api.pluginSetEnabled(id, enabled);
    await get().load();
  },
}));

export type { PluginContributions };
