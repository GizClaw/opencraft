// Tier-1 E2E backend mock: installs `window.go.bindings.*` and
// `window.runtime` so the real frontend runs against a scriptable stub.
// The function is deliberately self-contained (no module references) so
// Playwright can serialize it into the page via addInitScript.

export interface MockConfig {
  workspace?: string;
  currentSession?: string;
  startTurn?: { run_id: string; context_id: string };
  // Per-session turn ids, keyed by conversation id. Falls back to
  // startTurn, then to an auto-incrementing id.
  startTurns?: Record<string, { run_id: string; context_id?: string }>;
  // NewChat responses consumed in order, then an auto-incrementing id.
  newChatIds?: string[];
  // Per-conversation archive and per-run archive responses used by
  // resume and turn_end reconciliation.
  sessionTurnsByID?: Record<string, unknown[]>;
  turnByRunID?: Record<string, unknown>;
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
  let newChatSeq = 0;
  let startTurnSeq = 0;
  const listeners: Record<string, Array<(data: unknown) => void>> = {};

  const emit = (name: string, data: unknown) => {
    for (const cb of listeners[name] ?? []) cb(data);
  };
  win.__emit = emit;

  type Handler = (...args: any[]) => Promise<unknown>;

  const emptyList: Handler = async () => [];
  const noop: Handler = async () => undefined;

  const defaults: Record<string, Record<string, Handler>> = {
    Agent: {
      Detail: async () => null,
      List: emptyList,
      Unregister: noop,
      Update: noop,
    },
    Automation: {
      AutomationSessions: emptyList,
      Delete: noop,
      List: async () => config.automations ?? [],
      RunNow: noop,
      Runs: emptyList,
      Save: async (task: unknown) => task,
    },
    Config: {
      ConfigState: async () => ({}),
      ConfigStatus: async () => ({
        needed: false,
        default_model: 'test/model',
        default_reasoning: true,
        work_dir: config.workspace ?? '/workspace',
        user_dir: '/user',
        version: '0.1.0-test',
        agents: 0,
      }),
      MCPConfig: async () => [],
      MCPStatus: emptyList,
      MemoryConfig: async () => ({}),
      ModelCatalog: emptyList,
      ModelOptions: emptyList,
      ModelUsage: emptyList,
      ModelUsageSeries: emptyList,
      Providers: emptyList,
      Reload: noop,
      SaveInstances: noop,
      SaveMCP: noop,
      SaveMemory: noop,
      TestMCP: noop,
      Version: async () => '0.1.0-test',
    },
    Conversation: {
      CancelTurn: noop,
      CurrentSession: async () => config.currentSession ?? 's-1',
      NewChat: async () => {
        const queued = config.newChatIds?.shift();
        if (queued) return queued;
        newChatSeq += 1;
        return newChatSeq === 1 ? 's-new' : `s-new-${newChatSeq}`;
      },
      ReplyPrompt: async () => true,
      ResumeSession: noop,
      SessionMode: async () => 'workspace',
      SetSessionMode: noop,
      StartTurn: async (req: unknown) => {
        const contextID =
          (req as { context_id?: string } | undefined)?.context_id ??
          config.currentSession ??
          's-1';
        const mapped = config.startTurns?.[contextID];
        if (mapped) return { ...mapped, context_id: contextID };
        if (config.startTurn) return config.startTurn;
        return {
          run_id: `r-${++startTurnSeq}`,
          context_id: contextID,
        };
      },
    },
    Diagnostics: {
      ClearCaches: async () => ({ dirs: [], bytes: 0 }),
      Diagnostics: async () => ({}),
      EvaluateCommandPolicy: async () => ({ command: '', allowed: true }),
      RunSandboxProbe: async () => ({ ok: true }),
    },
    File: {
      Diff: async () => '',
      List: emptyList,
      OpenArtifactWith: noop,
      OpenExternal: noop,
      OpenPath: noop,
      PickFile: async () => '',
      PickFolder: async () => '',
      ReadAttachment: async () => null,
      ReadText: async () => '',
      RenderPatch: emptyList,
      Reveal: noop,
      SaveArtifactAs: async () => '',
      Search: emptyList,
    },
    Lifecycle: {
      CloseRequested: async () => false,
      GetCloseToTray: async () => true,
      GetLanguage: async () => 'zh-CN',
      MarkQuitting: noop,
      QuitFromTray: noop,
      RequestClose: noop,
      SetCloseToTray: noop,
      SetLanguage: noop,
      ShowMainWindow: noop,
    },
    Plugin: {
      ApplyUpdate: async () => null,
      Bundle: async () => '',
      CheckUpdate: async () => null,
      Inspect: async () => null,
      Install: async () => null,
      InstallZip: async () => null,
      Invoke: async () => '',
      KVDelete: noop,
      KVGet: async () => ({ key: '', value: '' }),
      KVList: emptyList,
      KVSet: noop,
      List: emptyList,
      Rollback: async () => null,
      SetEnabled: noop,
      Skills: emptyList,
      Tools: emptyList,
      Uninstall: noop,
      Update: async () => null,
      UpdateZip: async () => null,
    },
    Secret: {
      Delete: noop,
      Exists: async () => false,
      Set: noop,
    },
    Session: {
      ActiveRun: async () => '',
      ConversationDelegationCards: emptyList,
      DelegationCards: emptyList,
      Delete: noop,
      Exists: async () => false,
      ExportBundle: async () => '',
      ExportMarkdown: async () => '',
      History: emptyList,
      ImportBundle: async () => '',
      List: async () => config.listSessions ?? [],
      Rename: noop,
      Turns: async (id: string) =>
        config.sessionTurnsByID?.[id] ?? config.sessionTurns ?? [],
      TurnByRunID: async (_id: string, runID: string) => {
        const turn = config.turnByRunID?.[runID];
        if (!turn) {
          throw new Error(`TurnByRunID: archive turn not found for ${runID}`);
        }
        return turn;
      },
    },
    Settings: {
      AllowPermission: noop,
      CancelCard: async () => false,
      DeleteSkill: noop,
      DenyPermission: noop,
      GetModel: async () => '',
      GetThink: async () => 'medium',
      InstallSkill: async () => '',
      Permissions: emptyList,
      ReadLog: async () => '',
      RenderSkillPatch: emptyList,
      SetModel: noop,
      SetThink: noop,
      SkillContent: async () => '',
      Skills: emptyList,
    },
    Workspace: {
      Active: async () => config.workspace ?? '/workspace',
      ChooseWorkspace: async () => config.workspace ?? '/workspace',
      List: emptyList,
      Open: noop,
      Remove: noop,
    },
  };

  // Old single-App method names still accepted in handlers so existing
  // e2e overrides do not need a full rename at once.
  const legacy: Record<string, [string, string]> = {
    AgentDetail: ['Agent', 'Detail'],
    AutomationRuns: ['Automation', 'Runs'],
    Automations: ['Automation', 'List'],
    CancelTurn: ['Conversation', 'CancelTurn'],
    ConfigState: ['Config', 'ConfigState'],
    ConfigStatus: ['Config', 'ConfigStatus'],
    ConversationDelegationCards: ['Session', 'ConversationDelegationCards'],
    CurrentSession: ['Conversation', 'CurrentSession'],
    DelegationCards: ['Session', 'DelegationCards'],
    DeleteAutomation: ['Automation', 'Delete'],
    DeleteSession: ['Session', 'Delete'],
    DeleteSkill: ['Settings', 'DeleteSkill'],
    FileDiff: ['File', 'Diff'],
    GetModel: ['Settings', 'GetModel'],
    GetThink: ['Settings', 'GetThink'],
    InstallSkill: ['Settings', 'InstallSkill'],
    ListAgents: ['Agent', 'List'],
    ListDir: ['File', 'List'],
    ListSessions: ['Session', 'List'],
    ModelCatalog: ['Config', 'ModelCatalog'],
    ModelOptions: ['Config', 'ModelOptions'],
    NewChat: ['Conversation', 'NewChat'],
    OpenWorkspace: ['Workspace', 'Open'],
    Permissions: ['Settings', 'Permissions'],
    PickFile: ['File', 'PickFile'],
    PickFolder: ['File', 'PickFolder'],
    PluginApplyUpdate: ['Plugin', 'ApplyUpdate'],
    PluginBundle: ['Plugin', 'Bundle'],
    PluginCheckUpdate: ['Plugin', 'CheckUpdate'],
    PluginInspect: ['Plugin', 'Inspect'],
    PluginInstall: ['Plugin', 'Install'],
    PluginInstallZip: ['Plugin', 'InstallZip'],
    PluginInvoke: ['Plugin', 'Invoke'],
    PluginKVDelete: ['Plugin', 'KVDelete'],
    PluginKVGet: ['Plugin', 'KVGet'],
    PluginKVList: ['Plugin', 'KVList'],
    PluginKVSet: ['Plugin', 'KVSet'],
    PluginList: ['Plugin', 'List'],
    PluginRollback: ['Plugin', 'Rollback'],
    PluginSetEnabled: ['Plugin', 'SetEnabled'],
    PluginSkills: ['Plugin', 'Skills'],
    PluginTools: ['Plugin', 'Tools'],
    PluginUninstall: ['Plugin', 'Uninstall'],
    PluginUpdate: ['Plugin', 'Update'],
    PluginUpdateZip: ['Plugin', 'UpdateZip'],
    ReadAttachment: ['File', 'ReadAttachment'],
    ReadFile: ['File', 'ReadText'],
    RemoveWorkspace: ['Workspace', 'Remove'],
    RenameSession: ['Session', 'Rename'],
    ReplyPrompt: ['Conversation', 'ReplyPrompt'],
    ResumeSession: ['Conversation', 'ResumeSession'],
    RevealArtifact: ['File', 'Reveal'],
    RunAutomationNow: ['Automation', 'RunNow'],
    SaveAutomation: ['Automation', 'Save'],
    SearchFiles: ['File', 'Search'],
    SecretDelete: ['Secret', 'Delete'],
    SecretExists: ['Secret', 'Exists'],
    SessionHistory: ['Session', 'History'],
    SessionMode: ['Conversation', 'SessionMode'],
    SessionTurns: ['Session', 'Turns'],
    SetModel: ['Settings', 'SetModel'],
    SetThink: ['Settings', 'SetThink'],
    Skills: ['Settings', 'Skills'],
    StartTurn: ['Conversation', 'StartTurn'],
    UnregisterAgent: ['Agent', 'Unregister'],
    UpdateAgent: ['Agent', 'Update'],
    Version: ['Config', 'Version'],
    Workspace: ['Workspace', 'Active'],
    Workspaces: ['Workspace', 'List'],
  };

  const overrides: Record<string, Record<string, Handler>> = {};
  for (const [key, fn] of Object.entries(config.handlers ?? {})) {
    if (key.includes('.')) {
      const [module, method] = key.split('.');
      (overrides[module] ??= {})[method] = fn;
      continue;
    }
    const target = legacy[key];
    if (target) {
      (overrides[target[0]] ??= {})[target[1]] = fn;
    } else {
      // Unique old name fallback: apply to every module with the same
      // method name.
      for (const module of Object.keys(defaults)) {
        if (defaults[module][key]) {
          (overrides[module] ??= {})[key] = fn;
        }
      }
    }
  }

  const modules: Record<string, Record<string, Handler>> = {};
  for (const [name, methods] of Object.entries(defaults)) {
    const merged = { ...methods, ...(overrides[name] ?? {}) };
    modules[name] = new Proxy(merged, {
      get(target, prop) {
        if (typeof prop === 'string' && prop in target) {
          return target[prop];
        }
        // Unknown bindings resolve so bootstrapping never rejects.
        return noop;
      },
    });
  }
  win.go = { bindings: modules };

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
