// Plugin contracts for the frontend plugin host. Plugins are loaded
// from ~/.opencraft/plugins/<id>/ by the Go bindings (PluginList /
// PluginBundle / PluginSetEnabled); the host evaluates the entry
// bundle with an injected ctx and collects its contributions.

import type { ComponentType } from 'react';

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

export interface PluginContributions {
  settingsPanels?: SettingsPanelContribution[];
  sidebarEntries?: SidebarEntryContribution[];
}

// PluginCtx is the sealed API handed to a plugin bundle. It exposes
// only what the manifest permissions allow and never carries secrets.
export interface PluginCtx {
  React: typeof import('react');
  contribute: (c: PluginContributions) => void;
  ui: {
    flash: (text: string) => void;
  };
  // secrets is injected only when the plugin declares the
  // "secrets:auth" permission. It is bound to the "auth" scope: the
  // plugin can check existence and delete, never read the value.
  secrets?: {
    has: (name: string) => Promise<boolean>;
    delete: (name: string) => Promise<void>;
  };
}
