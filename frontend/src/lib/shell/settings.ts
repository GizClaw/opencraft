import type { StoreApi } from 'zustand';
import i18n from '../../i18n';
import { activeConversationID } from '../conv/core';
import { api } from '../api';
import { applyUISettings, cacheUISettings } from '../appearance';
import { createConvCore } from '../conv/core';
import { measureInteraction } from '../perfMetrics';
import type { StoreState } from '../store';

// Window-level settings: the config page and palette toggles, the
// conversation's think/model/mode, the UI settings document and the
// theme. S4 moved these out of store.ts (move only); the theme
// resolver stays in store.ts and is passed in, so this module never
// imports the store it is wired into.

export function createShellSettings(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
  applyTheme: (theme: 'dark' | 'light' | 'auto') => 'dark' | 'light',
): Pick<
  StoreState,
  | 'openConfig'
  | 'closeConfig'
  | 'openPalette'
  | 'closePalette'
  | 'togglePalette'
  | 'openShortcuts'
  | 'closeShortcuts'
  | 'toggleShortcuts'
  | 'setMode'
  | 'setThink'
  | 'setModel'
  | 'setTheme'
  | 'setUISettings'
  | 'setSessionDefaults'
  | 'refreshAgents'
> {
  const set = store.setState;
  const get = store.getState;
  const { updateConv } = core;

  return {
    openConfig: (tab) => {
      // Opening the settings page is a render the probe attributes: the
      // measurement runs to the frame that shows it.
      void measureInteraction('settings-open', () =>
        set({ configOpen: true, configTab: tab ?? 'general' }),
      );
    },

    closeConfig: () => set({ configOpen: false }),

    openPalette: () => set({ paletteOpen: true }),

    closePalette: () => set({ paletteOpen: false }),

    togglePalette: () => set((state) => ({ paletteOpen: !state.paletteOpen })),

    openShortcuts: () => set({ shortcutsOpen: true }),

    closeShortcuts: () => set({ shortcutsOpen: false }),

    toggleShortcuts: () =>
      set((state) => ({ shortcutsOpen: !state.shortcutsOpen })),

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        const convID = activeConversationID();
        if (convID) updateConv(convID, { mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setThink: async (level) => {
      try {
        await api.setThink(level);
        const convID = activeConversationID();
        if (convID) updateConv(convID, { think: level });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setModel: async (model) => {
      try {
        await api.setModel(model);
        const convID = activeConversationID();
        if (!convID) return;
        const previous = get().conversations[convID]?.model ?? '';
        updateConv(convID, { model });
        // Provider prompt caches are scoped to the model, so switching
        // mid-conversation re-reads the whole transcript at undiscounted
        // input price once. Worth one line: the user otherwise reads the
        // bill (or the sudden latency) as a bug. A conversation that has
        // not exchanged anything yet has no cache to lose, so it stays
        // quiet.
        const hasContent =
          (get().conversations[convID]?.messages.length ?? 0) > 0;
        if (hasContent && previous !== '' && previous !== model) {
          get().toast(i18n.t('chat.modelSwitchCost', { model }), 'warning');
        }
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setTheme: (theme) => {
      const resolved = applyTheme(theme);
      window.localStorage.setItem('opencraft.theme', theme);
      set({ theme, resolvedTheme: resolved });
    },

    setUISettings: (settings) => {
      applyUISettings(settings);
      cacheUISettings(settings);
      set({ uiSettings: settings });
    },

    setSessionDefaults: (d) => set({ sessionDefaults: d }),

    refreshAgents: async () => {
      try {
        set({ agents: (await api.listAgents()) ?? [] });
      } catch {
        // best-effort
      }
    },
  };
}
