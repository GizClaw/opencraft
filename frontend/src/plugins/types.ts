// Plugin contracts for the OpenCraft plugin host.
//
// The authoring protocol IS Cordis (the meta-framework used by
// deepseek-harness / Koishi). A plugin is an ES module exporting:
//
//   export const inject = ['storage', 'react'];  // optional
//   export function apply(ctx: Context) { ... }  // required
//
// The host loads the bundle, assembles it into a Cordis plugin object
// ({ name, inject, apply }) and mounts it with `ctx.plugin()`, so
// plugins get real Cordis semantics: injectable services, scoped
// contexts, ctx.effect / ctx.on reversible registrations, typed
// events and per-plugin teardown.
//
// The host provides its services on the root Context. Permission-
// gated services (storage/secrets/auth/inference) are isolated from a
// plugin's branch unless the manifest grants the matching permission,
// so an unpermitted service resolves to undefined (fail-closed); the
// inject list is validated against the manifest before loading.
// ctx.on is a core Cordis primitive and is always available (the old
// "events:subscribe" permission is no longer required).

import type { ComponentType } from 'react';
import type { Context } from '@cordisjs/core';

export type { Context };

export interface PluginKVEntry {
  key: string;
  value: string;
}

export interface PluginSummary {
  id: string;
  name: string;
  version: string;
  entry: string;
  permissions: string[];
  enabled: boolean;
  /** Builtin plugins ship with the app and can only be disabled. */
  builtin?: boolean;
  error?: string;
  panels?: string[];
  entries?: string[];
}

export interface SettingsPanelContribution {
  id: string;
  title: string;
  order: number;
  /** Settings tab to render in; defaults to "plugins". */
  tab?: string;
  Component: ComponentType;
}

export interface SidebarEntryContribution {
  id: string;
  title: string;
  order: number;
  onClick: () => void;
}

export interface CommandContribution {
  id: string;
  title: string;
  order: number;
  run: () => void;
}

export interface StatusBarContribution {
  id: string;
  order: number;
  Component: ComponentType;
}

// ---- host-provided services ----

export interface UIService {
  /** Shows a transient status message. Always available. */
  flash: (text: string) => void;
}

// storage is injected with "storage:kv": the plugin's own namespaced
// key/value store (non-secret data), scoped to the calling plugin.
export interface KVService {
  get: (key: string) => Promise<string | null>;
  set: (key: string, value: string) => Promise<void>;
  delete: (key: string) => Promise<void>;
  list: () => Promise<Record<string, string>>;
}

// secrets is injected with "secrets:auth", bound to the auth scope:
// existence check and delete only, never the value.
export interface SecretsService {
  has: (name: string) => Promise<boolean>;
  delete: (name: string) => Promise<void>;
}

/** add() registers one contribution and returns its disposer. */
export interface Registrar<T> {
  add: (contribution: T) => () => void;
}

export type PluginServiceKey =
  // always available
  | 'react'
  | 'ui'
  // permission-gated
  | 'storage'
  | 'secrets'
  // contribution points (always available)
  | 'settingsPanels'
  | 'sidebarEntries'
  | 'commands'
  | 'statusBar';

// The exported module shape of a plugin bundle (ESM).
export interface PluginModule {
  name?: string;
  inject?: PluginServiceKey[];
  apply: (ctx: Context) => void;
}

// ---- host UI events (ctx.on / ctx.emit over the shared bus) ----

export interface TurnEndEvent {
  status?: string;
}

export interface InteractEvent {
  title?: string;
}

// Cordis-style typed events: plugin authors get type-checked ctx.on
// subscriptions through this augmentation.
declare module '@cordisjs/core' {
  interface Context {
    /** The host React runtime — the same instance that renders the app. */
    react: typeof import('react');
    ui: UIService;
    storage: KVService;
    secrets: SecretsService;
    settingsPanels: Registrar<SettingsPanelContribution>;
    sidebarEntries: Registrar<SidebarEntryContribution>;
    commands: Registrar<CommandContribution>;
    statusBar: Registrar<StatusBarContribution>;
    /**
     * Invokes a method on this plugin's capability subprocess (if the
     * manifest declares one). params and the result are JSON; the host
     * only routes by method name and never interprets the semantics.
     */
    invoke: (method: string, params?: unknown) => Promise<unknown>;
    /**
     * The host's current language (e.g. "zh" / "en"). Plugins ship
     * their own dictionaries and render through this live value.
     */
    i18n: { locale: string };
  }

  interface Events {
    turn_end(data: TurnEndEvent): void;
    interact(data: InteractEvent): void;
  }
}
