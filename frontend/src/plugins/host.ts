import * as React from 'react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import { useStore as useMainStore } from '../lib/store';
import type { UIEvent } from '../lib/types';
import type {
  PluginCapabilities,
  PluginContributions,
  PluginCtx,
} from './types';

// eventBus forwards host UI events to plugin subscribers. One wails
// subscription is shared by every plugin.
const eventHandlers = new Map<string, Set<(data: unknown) => void>>();
let eventBusAttached = false;

function attachEventBus() {
  if (eventBusAttached) return;
  eventBusAttached = true;
  EventsOn('opencraft:ui', (ev: UIEvent) => {
    const handlers = eventHandlers.get(ev.type);
    if (!handlers) return;
    for (const h of [...handlers]) {
      try {
        h(ev.data);
      } catch {
        // A plugin handler must never break the event loop.
      }
    }
  });
}

// createPluginCtx builds the injected context for one plugin bundle.
// Plugins only ever see this object (plus their own closure); they do
// not get window.go or the full store.
export function createPluginCtx(
  pluginID: string,
  contribute: (c: PluginContributions) => void,
  permissions: string[],
): PluginCtx {
  const capabilities: PluginCapabilities = {
    ui: {
      flash: (text: string) => useMainStore.getState().flash(text),
    },
  };
  if (permissions.includes('storage:kv')) {
    capabilities.storage = {
      get: async (key) => {
        const entry = await api.pluginKVGet(pluginID, key);
        return entry.value || null;
      },
      set: (key, value) => api.pluginKVSet(pluginID, key, value),
      delete: (key) => api.pluginKVDelete(pluginID, key),
      list: async () => {
        const entries = await api.pluginKVList(pluginID);
        const out: Record<string, string> = {};
        for (const e of entries) out[e.key] = e.value;
        return out;
      },
    };
  }
  if (permissions.includes('secrets:auth')) {
    capabilities.secrets = {
      has: (name: string) => api.secretExists('auth', name),
      delete: (name: string) => api.secretDelete('auth', name),
    };
  }
  if (permissions.includes('events:subscribe')) {
    attachEventBus();
    capabilities.events = {
      on: (type: string, handler: (data: unknown) => void) => {
        let set = eventHandlers.get(type);
        if (!set) {
          set = new Set();
          eventHandlers.set(type, set);
        }
        set.add(handler);
        return () => {
          set?.delete(handler);
        };
      },
    };
  }
  return {
    React,
    contribute,
    capabilities,
  };
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
    commands: [],
    statusBar: [],
  };
  const ctx = createPluginCtx(id, (c) => {
    contributed.settingsPanels?.push(...(c.settingsPanels ?? []));
    contributed.sidebarEntries?.push(...(c.sidebarEntries ?? []));
    contributed.commands?.push(...(c.commands ?? []));
    contributed.statusBar?.push(...(c.statusBar ?? []));
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
