// Tier-1 E2E backend mock: installs `window.go.desktop.App` and
// `window.runtime` so the real frontend runs against a scriptable stub.
// The function is deliberately self-contained (no module references) so
// Playwright can serialize it into the page via addInitScript.

export interface MockConfig {
  workspace?: string;
  currentSession?: string;
  startTurn?: { run_id: string; context_id: string };
  listSessions?: unknown[];
  sessionTurns?: unknown[];
  automations?: unknown[];
  // Per-method overrides, e.g. { ReadFile: async () => '...' }
  handlers?: Record<string, (...args: any[]) => Promise<unknown>>;
}

export function mockBackend(cfg?: MockConfig) {
  const win = window as unknown as Record<string, unknown> & {
    __emit?: (name: string, data: unknown) => void;
  };
  const config: MockConfig = cfg ?? {};
  const listeners: Record<string, Array<(data: unknown) => void>> = {};

  const emit = (name: string, data: unknown) => {
    for (const cb of listeners[name] ?? []) cb(data);
  };
  win.__emit = emit;

  const app: Record<string, (...args: any[]) => Promise<unknown>> = {
    Version: async () => '0.1.0-test',
    ConfigStatus: async () => ({
      needed: false,
      default_model: 'test/model',
      default_reasoning: true,
      work_dir: config.workspace ?? '/workspace',
      user_dir: '/user',
      version: '0.1.0-test',
      agents: 0,
    }),
    Workspace: async () => config.workspace ?? '/workspace',
    ProjectConfigStatus: async () => null,
    CurrentSession: async () => config.currentSession ?? 's-1',
    NewChat: async () => ({
      session_id: 's-new',
      mode: 'workspace',
      think: 'medium',
      model: '',
    }),
    ResumeSession: async (id: string) => ({
      session_id: id,
      mode: 'workspace',
      think: 'medium',
      model: '',
    }),
    SessionMode: async () => 'workspace',
    GetThink: async () => 'medium',
    GetModel: async () => '',
    ModelOptions: async () => [],
    ListSessions: async () => config.listSessions ?? [],
    SessionTurns: async () => config.sessionTurns ?? [],
    Workspaces: async () => [],
    Providers: async () => [],
    ConfigState: async () => ({}),
    ModelCatalog: async () => [],
    Skills: async () => [],
    Permissions: async () => [],
    PluginList: async () => [],
    Automations: async () => config.automations ?? [],
    AutomationRuns: async () => [],
    ListAgents: async () => [],
    DelegationCards: async () => [],
    ConversationDelegationCards: async () => [],
    StartTurn: async () =>
      config.startTurn ?? { run_id: 'r-1', context_id: 's-1' },
    UndoState: async () => ({ can_undo: false, can_redo: false }),
    UndoChange: async () => [],
    RedoChange: async () => [],
    ReadFile: async () => '',
    FileDiff: async () => '',
    ReadAttachment: async () => null,
    PickFile: async () => '',
    ...(config.handlers ?? {}),
  };

  const handler = new Proxy(app, {
    get(target, prop) {
      if (typeof prop === 'string' && prop in target) {
        return target[prop];
      }
      // Unknown bindings resolve so bootstrapping never rejects.
      return async () => undefined;
    },
  });
  win.go = { desktop: { App: handler } };

  const runtimeBase = {
    EventsOn: (name: string, cb: (data: unknown) => void) => {
      (listeners[name] ??= []).push(cb);
      return () => {
        listeners[name] = (listeners[name] ?? []).filter((c) => c !== cb);
      };
    },
    EventsOnMultiple: (name: string, cb: (data: unknown) => void) => {
      (listeners[name] ??= []).push(cb);
      return () => {};
    },
    EventsOnce: (name: string, cb: (data: unknown) => void) => {
      const off = runtime.EventsOn(name, (data) => {
        off();
        cb(data);
      });
    },
    EventsOff: (name: string) => {
      delete listeners[name];
    },
    EventsOffAll: () => {
      for (const k of Object.keys(listeners)) delete listeners[k];
    },
    EventsEmit: (name: string, data: unknown) => emit(name, data),
    Environment: async () => ({
      platform: 'darwin',
      arch: 'arm64',
      name: 'opencraft',
      version: '0.1.0-test',
    }),
    SendNotification: async () => {},
    SendNotificationWithActions: async () => {},
    BrowserOpenURL: () => {},
    OnFileDrop: () => {},
    OnFileDropOff: () => {},
  };
  // Any other runtime binding (notifications, window controls, logs)
  // is a no-op so bootstrapping never rejects.
  const runtime = new Proxy(runtimeBase, {
    get(target, prop) {
      if (typeof prop === 'string' && prop in target) {
        return target[prop];
      }
      // Unknown runtime bindings (notifications, window controls, logs)
      // are async no-ops so both sync and promise-based callers work.
      return async () => {};
    },
  });
  win.runtime = runtime;
}
