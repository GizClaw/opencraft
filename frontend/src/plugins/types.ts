// Plugin contracts for the frontend plugin host. Plugins are loaded
// from ~/.opencraft/plugins/<id>/ by the Go bindings (PluginList /
// PluginBundle / PluginSetEnabled); the host evaluates the entry
// bundle with an injected ctx and collects its contributions.

import type { ComponentType } from 'react';

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

export interface PluginContributions {
  settingsPanels?: SettingsPanelContribution[];
  sidebarEntries?: SidebarEntryContribution[];
  commands?: CommandContribution[];
  statusBar?: StatusBarContribution[];
}

// PluginCapabilities is the permission-gated runtime API handed to a
// plugin. Capabilities are objects registered by the host, not
// hardcoded plugin methods; the manifest permissions decide which ones
// exist.
export interface PluginCapabilities {
  ui: {
    flash: (text: string) => void;
  };
  // storage is injected with "storage:kv": the plugin's own
  // namespaced key/value store (non-secret data).
  storage?: {
    get: (key: string) => Promise<string | null>;
    set: (key: string, value: string) => Promise<void>;
    delete: (key: string) => Promise<void>;
    list: () => Promise<Record<string, string>>;
  };
  // secrets is injected with "secrets:auth", bound to the auth scope:
  // existence check and delete only, never the value.
  secrets?: {
    has: (name: string) => Promise<boolean>;
    delete: (name: string) => Promise<void>;
  };
  // events is injected with "events:subscribe": subscribe to host
  // UI events (turn_end, workspace_switch, ...) and receive their data.
  events?: {
    on: (type: string, handler: (data: unknown) => void) => () => void;
  };
}

// PluginCtx is the sealed API handed to a plugin bundle. It exposes
// only what the manifest permissions allow and never carries secrets.
export interface PluginCtx {
  React: typeof import('react');
  contribute: (c: PluginContributions) => void;
  capabilities: PluginCapabilities;
}
