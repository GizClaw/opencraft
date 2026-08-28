import * as React from 'react';
import { api } from '../lib/api';
import { useStore as useMainStore } from '../lib/store';
import type {
  PluginContributions,
  PluginCtx,
  SettingsPanelContribution,
  SidebarEntryContribution,
} from './types';

// createPluginCtx builds the injected context for one plugin bundle.
// Plugins only ever see this object (plus their own closure); they do
// not get window.go or the full store.
export function createPluginCtx(
  contribute: (c: PluginContributions) => void,
  permissions: string[],
): PluginCtx {
  const ctx: PluginCtx = {
    React,
    contribute,
    ui: {
      flash: (text: string) => useMainStore.getState().flash(text),
    },
  };
  if (permissions.includes('secrets:auth')) {
    ctx.secrets = {
      has: (name: string) => api.secretExists('auth', name),
      delete: (name: string) => api.secretDelete('auth', name),
    };
  }
  return ctx;
}

// evaluatePlugin runs one bundle in the page context and collects its
// contributions. This is the documented transition sandbox (Phase 0);
// the target is an iframe + postMessage bridge.
export function evaluatePlugin(
  id: string,
  src: string,
  permissions: string[],
): PluginContributions {
  const contributed: PluginContributions = {
    settingsPanels: [],
    sidebarEntries: [],
  };
  const ctx = createPluginCtx((c) => {
    contributed.settingsPanels?.push(...(c.settingsPanels ?? []));
    contributed.sidebarEntries?.push(...(c.sidebarEntries ?? []));
  }, permissions);
  try {
    // eslint-disable-next-line @typescript-eslint/no-implied-eval
    new Function('ctx', src)(ctx);
  } catch (err) {
    throw new Error(`plugin ${id} failed to activate: ${String(err)}`);
  }
  return contributed;
}

export function sortByOrder<T extends { order: number }>(a: T, b: T): number {
  return a.order - b.order;
}

export type { SettingsPanelContribution, SidebarEntryContribution };
