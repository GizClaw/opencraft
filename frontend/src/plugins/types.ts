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

export interface AuthUser {
  name: string;
  email: string;
  department: string;
}

export interface AuthBeginResult {
  provider: string;
  verification_uri: string;
  verification_uri_complete: string;
  user_code: string;
  interval_sec: number;
  expires_at: string;
}

export interface AuthPollResult {
  status: string;
  message?: string;
  retry_after_sec?: number;
  user?: AuthUser;
  default_model?: string;
}

export interface AuthStatusResult {
  status: string;
  user?: AuthUser;
  expires_at?: string;
  default_model?: string;
  model_count?: number;
}

export interface PluginSummary {
  id: string;
  name: string;
  version: string;
  entry: string;
  permissions: string[];
  enabled: boolean;
  error?: string;
  panels?: string[];
  entries?: string[];
}

export interface SettingsPanelContribution {
  id: string;
  title: string;
  order: number;
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

// auth is injected with "auth:device": device-authorization primitives.
// device_code and tokens stay Go-side.
export interface AuthService {
  begin: (provider: string, clientID?: string) => Promise<AuthBeginResult>;
  poll: (provider: string) => Promise<AuthPollResult>;
  rotate: (provider: string) => Promise<void>;
  revoke: (provider: string) => Promise<void>;
  status: (provider: string) => Promise<AuthStatusResult>;
  me: (provider: string) => Promise<AuthUser>;
  models: (provider: string) => Promise<string[]>;
}

// inference is injected with "inference:upsert": wire the completed
// auth session into the inference config.
export interface InferenceService {
  upsertGatewayProfile: (
    providerID: string,
    displayName?: string,
  ) => Promise<void>;
  removeGatewayProfile: (providerID: string) => Promise<void>;
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
  | 'auth'
  | 'inference'
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
    auth: AuthService;
    inference: InferenceService;
    settingsPanels: Registrar<SettingsPanelContribution>;
    sidebarEntries: Registrar<SidebarEntryContribution>;
    commands: Registrar<CommandContribution>;
    statusBar: Registrar<StatusBarContribution>;
  }

  interface Events {
    turn_end(data: TurnEndEvent): void;
    interact(data: InteractEvent): void;
  }
}
