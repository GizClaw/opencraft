import * as React from 'react';
import { Context } from '@cordisjs/core';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import i18n from '../i18n';
import { api } from '../lib/api';
import { useStore as useMainStore } from '../lib/store';
import type { UIEvent } from '../lib/types';
import type {
  CommandContribution,
  KVService,
  PluginModule,
  PluginServiceKey,
  Registrar,
  SecretsService,
  SettingsPanelContribution,
  SidebarEntryContribution,
  StatusBarContribution,
  UIService,
} from './types';

// Services that are gated by a manifest permission. A plugin branch
// isolates each unpermitted service, so it resolves to undefined there.
const PERMISSION_GATED: Partial<Record<PluginServiceKey, string>> = {
  storage: 'storage:kv',
  secrets: 'secrets:auth',
};

// Services that are always provided, no permission needed.
const ALWAYS_SERVICES: PluginServiceKey[] = [
  'react',
  'ui',
  'settingsPanels',
  'sidebarEntries',
  'commands',
  'statusBar',
];

const KNOWN_SERVICES = new Set<PluginServiceKey>([
  ...(Object.keys(PERMISSION_GATED) as PluginServiceKey[]),
  ...ALWAYS_SERVICES,
]);

export interface ContributionState {
  settingsPanels: SettingsPanelContribution[];
  sidebarEntries: SidebarEntryContribution[];
  commands: CommandContribution[];
  statusBar: StatusBarContribution[];
}

export interface PluginActivation {
  id: string;
  dispose: () => void;
}

// The live Cordis app. Recreated on every load cycle; plugins mounted
// into it are torn down with their scopes when the app is reset.
let app: Context | null = null;
let contributions: ContributionState | null = null;

// Host UI events are forwarded into the Cordis app through one shared
// wails subscription; plugins subscribe with ctx.on(type, handler).
let eventBusAttached = false;

function attachEventBus() {
  if (eventBusAttached) return;
  eventBusAttached = true;
  EventsOn('opencraft:ui', (ev: UIEvent) => {
    (app?.emit as (type: string, data: unknown) => void)(ev.type, ev.data);
  });
}

function callerOf(this: unknown) {
  const ctx = (this as { ctx?: Context } | null | undefined)?.ctx;
  if (!ctx) throw new Error('service called outside a plugin scope');
  return ctx;
}

function makeKVService(): KVService {
  return {
    get: async function (this: unknown, key: string) {
      const id = callerOf.call(this).config.id;
      const entry = await api.pluginKVGet(id, key);
      return entry.value || null;
    },
    set: function (this: unknown, key: string, value: string) {
      const id = callerOf.call(this).config.id;
      return api.pluginKVSet(id, key, value);
    },
    delete: function (this: unknown, key: string) {
      const id = callerOf.call(this).config.id;
      return api.pluginKVDelete(id, key);
    },
    list: async function (this: unknown) {
      const id = callerOf.call(this).config.id;
      const entries = await api.pluginKVList(id);
      const out: Record<string, string> = {};
      for (const e of entries) out[e.key] = e.value;
      return out;
    },
  };
}

function makeSecretsService(): SecretsService {
  return {
    has: (name: string) => api.secretExists('auth', name),
    delete: (name: string) => api.secretDelete('auth', name),
  };
}

// Contribution registrars are provided as traced services: when a
// plugin calls ctx.settingsPanels.add(...), `this.ctx` inside the
// method is the caller's scoped context, so the contribution is tied
// to the plugin scope via ctx.effect and removed on plugin teardown.
function makeRegistrar<T>(arr: T[]): Registrar<T> {
  return {
    add: function (this: unknown, item: T) {
      const ctx = callerOf.call(this);
      arr.push(item);
      const dispose = () => {
        const i = arr.indexOf(item);
        if (i >= 0) arr.splice(i, 1);
      };
      ctx.effect(() => dispose);
      return dispose;
    },
  };
}

function provideServices(ctx: Context, c: ContributionState) {
  // Builtin services are always available: plugins may use them
  // without declaring them in inject (no warning, no PENDING wait).
  // react is provided as a spread copy so the tracker can be attached
  // without touching the (possibly frozen) React module object; the
  // copied functions are the same references, so hooks still work.
  ctx.provide('react', { ...React }, true);
  ctx.provide(
    'ui',
    {
      flash: (text: string) => useMainStore.getState().flash(text),
    } satisfies UIService,
    true,
  );
  ctx.provide('settingsPanels', makeRegistrar(c.settingsPanels), true);
  ctx.provide('sidebarEntries', makeRegistrar(c.sidebarEntries), true);
  ctx.provide('commands', makeRegistrar(c.commands), true);
  ctx.provide('statusBar', makeRegistrar(c.statusBar), true);
  // invoke routes to this plugin's capability subprocess. It is an
  // accessor so the calling plugin's id is captured on access.
  ctx.accessor('invoke', {
    get: function (this: Context) {
      const id = (this.config as { id?: string } | undefined)?.id ?? '';
      return async (method: string, params?: unknown) => {
        const raw = await api.pluginInvoke(
          id,
          method,
          JSON.stringify(params ?? {}),
        );
        return raw ? JSON.parse(raw) : undefined;
      };
    },
  });
  // i18n exposes the host's current language so plugins can render
  // their own translated strings; reading ctx.i18n.locale always
  // returns the live value.
  ctx.accessor('i18n', {
    get: function () {
      return { locale: i18n.language };
    },
  });
  // Permission-gated services must be declared in inject; unpermitted
  // ones are isolated from the plugin branch (see activatePlugin).
  ctx.provide('storage', makeKVService());
  ctx.provide('secrets', makeSecretsService());
}

/**
 * Disposes the previous Cordis app (plugin scopes run their disposers)
 * and creates a fresh one with all host services provided. Called at
 * the start of every plugin load cycle.
 */
export async function resetHost() {
  if (app) {
    for (const key of [...app.registry.keys()]) {
      if (key) app.registry.delete(key);
    }
    await app.stop().catch(() => {});
  }
  app = new Context();
  contributions = {
    settingsPanels: [],
    sidebarEntries: [],
    commands: [],
    statusBar: [],
  };
  provideServices(app, contributions);
  attachEventBus();
}

/** Fires the 'ready' lifecycle after a load cycle. */
export async function startHost() {
  if (!app) return;
  await app.start().catch(() => {});
}

export function getContributions(): ContributionState {
  if (!contributions) throw new Error('plugin host not initialized');
  return contributions;
}

function loadPluginModule(id: string, src: string): Promise<PluginModule> {
  const url = URL.createObjectURL(new Blob([src], { type: 'text/javascript' }));
  return import(/* @vite-ignore */ url)
    .then((ns) => {
      const apply = ns.apply ?? defaultApply(ns.default);
      if (typeof apply !== 'function') {
        throw new Error('bundle must export apply(ctx)');
      }
      return {
        name: (ns.name as string | undefined) ?? defaultName(ns.default),
        inject: normalizeInject(id, ns.inject ?? defaultInject(ns.default)),
        apply: apply as PluginModule['apply'],
      };
    })
    .catch((err) => {
      throw new Error(`plugin ${id}: failed to load bundle: ${String(err)}`);
    })
    .finally(() => URL.revokeObjectURL(url));
}

function defaultApply(mod: unknown): unknown {
  if (typeof mod === 'function') return mod;
  if (mod && typeof mod === 'object') return (mod as PluginModule).apply;
  return undefined;
}

function defaultInject(mod: unknown): unknown {
  if (mod && typeof mod === 'object') return (mod as PluginModule).inject;
  return undefined;
}

function defaultName(mod: unknown): string | undefined {
  if (mod && typeof mod === 'object') return (mod as PluginModule).name;
  return undefined;
}

function normalizeInject(id: string, raw: unknown): PluginServiceKey[] {
  if (raw == null) return [];
  if (!Array.isArray(raw)) {
    throw new Error(`plugin ${id}: inject must be an array of service keys`);
  }
  const keys: PluginServiceKey[] = [];
  for (const k of raw) {
    if (typeof k !== 'string' || !KNOWN_SERVICES.has(k as PluginServiceKey)) {
      throw new Error(`plugin ${id}: unknown service in inject: ${String(k)}`);
    }
    keys.push(k as PluginServiceKey);
  }
  return keys;
}

function validateInject(
  id: string,
  inject: PluginServiceKey[],
  permissions: string[],
) {
  for (const key of inject) {
    const perm = PERMISSION_GATED[key];
    if (perm && !permissions.includes(perm)) {
      throw new Error(
        `plugin ${id}: service "${key}" needs permission "${perm}", not granted by manifest`,
      );
    }
  }
}

/**
 * Loads one plugin bundle and mounts it into the host Cordis app with
 * real Cordis semantics. Unpermitted services are isolated from the
 * plugin's branch (they resolve to undefined), the inject list is
 * validated against the manifest, and a failing apply() is reported
 * after the scope settles. Returns a disposer that unmounts the
 * plugin and runs its scoped effects in reverse order.
 */
export async function activatePlugin(
  id: string,
  src: string,
  permissions: string[],
): Promise<PluginActivation> {
  if (!app) throw new Error('plugin host not initialized');
  const mod = await loadPluginModule(id, src);
  const inject = mod.inject ?? [];
  validateInject(id, inject, permissions);

  let parent: Context = app;
  for (const [service, perm] of Object.entries(PERMISSION_GATED)) {
    if (!permissions.includes(perm!)) {
      parent = parent.isolate(service, Symbol(`opencraft.deny.${service}`));
    }
  }

  const plugin: PluginModule = {
    name: mod.name ?? id,
    inject,
    apply: mod.apply,
  };
  parent.plugin(plugin, { id, permissions });
  // Let the Cordis scope settle so a synchronous apply() failure lands
  // on the MainScope (status FAILED, error set).
  await Promise.resolve();

  const runtime = app.registry.get(plugin);
  if (runtime?.error) {
    const message =
      runtime.error instanceof Error
        ? runtime.error.message
        : String(runtime.error);
    app.registry.delete(plugin);
    throw new Error(`plugin ${id} failed to activate: ${message}`);
  }
  return {
    id,
    dispose: () => {
      if (app) app.registry.delete(plugin);
    },
  };
}

export function sortByOrder<T extends { order: number }>(a: T, b: T): number {
  return a.order - b.order;
}
