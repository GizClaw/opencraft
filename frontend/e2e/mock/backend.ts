// Tier-1 E2E backend mock for the Wails v3 runtime. The real frontend loads
// @wailsio/runtime, whose mock transport (installed by src/lib/mockBridge)
// routes binding calls to the module/method table installed here. UI events
// are delivered through window._wails.dispatchWailsEvent, which the runtime
// registers once its events module loads.
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
  listSessionsInWorkspace?: Record<string, unknown[]>;
  workspaces?: unknown[];
  sessionTurns?: unknown[];
  automations?: unknown[];
  // viewerFile drives File.ResolveTarget/ReadPreview for the file
  // viewer e2e flows. Handlers are plain data so the config survives
  // addInitScript serialization.
  viewerFile?: {
    path: string;
    rel: string;
    name: string;
    root?: string;
    media_type?: string;
    size?: number;
    text?: string;
  };
  // Pet surface config: the pack list the renderer picks from, the
  // base64 .riv the asset channel serves, the window position a drag
  // anchors on, and the mount report the settings panel reads back.
  // Pet bindings also log every call to window.__ocPetCalls and every
  // report to window.__ocPetReports so specs can assert them.
  petPacks?: unknown[];
  petAsset?: string;
  petPosition?: { x: number; y: number; ready: boolean };
  petDiagnostics?: unknown;
  petRuntimeStatus?: unknown;
  assistantCharacter?: string;
  petsEnabled?: boolean;
  /**
   * Per-method overrides, e.g. { 'File.ReadFile': async () => '...' }.
   *
   * Pass them through {@link handlerSources}: Playwright serializes the
   * addInitScript argument as data and silently drops functions nested
   * inside it, so a bare object literal here arrives empty. Source text
   * survives, and the mock revives it inside the page. A revived handler
   * is recompiled in the browser, so it cannot close over spec-file
   * variables — build anything it needs from `window` or literal values.
   */
  handlers?: Record<string, Handler | string>;
}

/** One mocked binding method. */
type Handler = (...args: any[]) => Promise<unknown>;

/**
 * Rewrites handler overrides as source text so they survive Playwright's
 * argument serialization. See {@link MockConfig.handlers}.
 */
export function handlerSources(
  handlers: Record<string, Handler>,
): Record<string, string> {
  return Object.fromEntries(
    Object.entries(handlers).map(([key, handler]) => [key, handler.toString()]),
  );
}

export function mockBackend(cfg?: MockConfig) {
  const win = window as unknown as Record<string, unknown> & {
    __emit?: (name: string, data: unknown) => void;
  };
  const config: MockConfig = cfg ?? {};
  let newChatSeq = 0;
  let startTurnSeq = 0;
  let forkSeq = 0;
  // Pet call log: the pet surface is driven by events, so the only way a
  // spec can see "the drag called SetPosition" is this recording.
  const petCalls: { method: string; args: unknown[] }[] = [];
  const petReports: unknown[] = [];
  const recordPet = (method: string, args: unknown[]) => {
    petCalls.push({ method, args });
  };
  const emit = (name: string, data: unknown) => {
    const wails = (
      win as { _wails?: { dispatchWailsEvent?: (e: unknown) => void } }
    )._wails;
    if (typeof wails?.dispatchWailsEvent === 'function') {
      wails.dispatchWailsEvent({ name, data });
    }
  };
  win.__emit = emit;

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
      Profile: async () => ({ yolo_only: false }),
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
      ForkTurn: async () => `s-fork-${++forkSeq}`,
      NewChat: async () => {
        const queued = config.newChatIds?.shift();
        newChatSeq += 1;
        const id =
          queued ?? (newChatSeq === 1 ? 's-new' : `s-new-${newChatSeq}`);
        // Mirrors the NewChatResult binding: the id plus the effective
        // session defaults applied at mint time.
        return {
          session_id: id,
          mode: 'workspace',
          think: 'medium',
          model: '',
        };
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
      MetricRange: async () => [],
      // Mirrors the wire shape of a Go nil slice: the repair reports no
      // list when the layer has nothing to remove.
      RepairConfigCompat: async () => ({
        file: '',
        backup: '',
        removed: null,
      }),
      ReportFrontendError: noop,
      ReportFrontendPerf: noop,
      RunSandboxProbe: async () => ({ ok: true }),
    },
    File: {
      Diff: async () => '',
      List: emptyList,
      OpenArtifactWith: noop,
      OpenExternal: async (url: string) => {
        (globalThis as { __extUrl?: string }).__extUrl = String(url);
      },
      OpenPath: noop,
      PickFile: async () => '',
      PickFolder: async () => '',
      ReadAttachment: async () => null,
      ReadPreview: async () =>
        config.viewerFile
          ? {
              path: config.viewerFile.path,
              rel: config.viewerFile.rel,
              root: config.viewerFile.root ?? 'workspace',
              name: config.viewerFile.name,
              size: config.viewerFile.size ?? 0,
              media_type: config.viewerFile.media_type ?? '',
              kind: 'text' as const,
              text: config.viewerFile.text ?? '',
            }
          : { kind: 'meta' as const, size: 0 },
      RenderPatch: emptyList,
      ResolveTarget: async () =>
        config.viewerFile
          ? {
              path: config.viewerFile.path,
              rel: config.viewerFile.rel,
              root: config.viewerFile.root ?? 'workspace',
              name: config.viewerFile.name,
              is_dir: false,
              size: config.viewerFile.size ?? 0,
              media_type: config.viewerFile.media_type ?? '',
            }
          : {
              path: '',
              rel: '',
              root: 'workspace',
              name: 'missing',
              is_dir: false,
              size: 0,
              media_type: '',
            },
      Reveal: noop,
      SaveArtifactAs: async () => '',
      Search: emptyList,
    },
    Lifecycle: {
      GetCloseToTray: async () => true,
      GetPetsSettings: async () => ({
        enabled: config.petsEnabled ?? true,
        assistantCharacter: config.assistantCharacter ?? 'assistant-default',
      }),
      ReportUserActivity: noop,
      RequestClose: noop,
      SetCloseToTray: noop,
      SetLanguage: noop,
      SetPetsSettings: noop,
    },
    Pet: {
      Activate: async () => recordPet('Activate', []),
      Activities: emptyList,
      Diagnostics: async () =>
        config.petDiagnostics ?? {
          drives: { attention: 60, energy: 40, comfort: 50 },
          mood: 'content',
          stats: { poke_count: 0 },
          disposition: 'roam',
          phase: 'idle',
          walking: false,
        },
      ListPacks: async () => config.petPacks ?? [],
      MoveBy: async (dx: number, dy: number) => recordPet('MoveBy', [dx, dy]),
      PackAsset: async (asset: string) => {
        recordPet('PackAsset', [asset]);
        return config.petAsset ?? '';
      },
      Poke: async () => recordPet('Poke', []),
      Position: async () =>
        config.petPosition ?? { x: 100, y: 100, ready: true },
      RegisterPack: noop,
      ReportRuntimeStatus: async (status: unknown) => {
        petReports.push(status);
        recordPet('ReportRuntimeStatus', [status]);
      },
      RuntimeStatus: async () => {
        if (config.petRuntimeStatus) {
          return { status: config.petRuntimeStatus, reported: true };
        }
        return {
          status: petReports[petReports.length - 1] ?? null,
          reported: petReports.length > 0,
        };
      },
      SetPosition: async (x: number, y: number) =>
        recordPet('SetPosition', [x, y]),
      SetRoamingPaused: noop,
      UnregisterPack: noop,
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
      Delete: noop,
      Exists: async () => false,
      ExportBundle: async () => '',
      ExportMarkdown: async () => '',
      History: emptyList,
      ImportBundle: async () => '',
      List: async () => config.listSessions ?? [],
      ListInWorkspace: async (workspace: string) =>
        config.listSessionsInWorkspace?.[workspace] ??
        config.listSessions ??
        [],
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
      DeleteSkill: noop,
      DenyPermission: noop,
      GetSessionDefaults: async () => ({ mode: 'workspace', think: 'medium' }),
      GetModel: async () => '',
      GetThink: async () => 'medium',
      InstallSkill: async () => '',
      Permissions: emptyList,
      ReadLog: async () => '',
      RenderSkillPatch: emptyList,
      SetSessionDefaults: noop,
      SetModel: noop,
      SetThink: noop,
      SkillContent: async () => '',
      Skills: emptyList,
    },
    Workspace: {
      Active: async () => config.workspace ?? '/workspace',
      ChooseWorkspace: async () => config.workspace ?? '/workspace',
      List: async () => {
        if (config.workspaces) return config.workspaces;
        const workDir = config.workspace ?? '/workspace';
        return [
          {
            id: 'ws-default',
            path: workDir,
            title: 'Workspace',
            last_opened: '2026-01-01T00:00:00Z',
          },
        ];
      },
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
    CurrentSession: ['Conversation', 'CurrentSession'],
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
  // Declared here, not at module scope: Playwright serializes this
  // function by source, so anything it closes over from the module would
  // be undefined inside the page.
  const reviveHandler = (handler: Handler | string): Handler =>
    typeof handler === 'string'
      ? (new Function(`return (${handler})`)() as Handler)
      : handler;
  for (const [key, raw] of Object.entries(config.handlers ?? {})) {
    const fn = reviveHandler(raw);
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
  // Wails v3 mock transport table. Struct names match the generated binding
  // modules (Settings, Session, ...); __ocCall is the browser-side entry used
  // by src/lib/mockBridge.
  const exposed = win as unknown as {
    __ocMockByModule: typeof modules;
    __ocCall: (qualified: string, args: unknown[]) => Promise<unknown>;
    __ocPetCalls: typeof petCalls;
    __ocPetReports: typeof petReports;
  };
  exposed.__ocMockByModule = modules;
  exposed.__ocPetCalls = petCalls;
  exposed.__ocPetReports = petReports;
  exposed.__ocCall = async (
    qualified: string,
    callArgs: unknown[],
  ): Promise<unknown> => {
    const match = /\.([A-Za-z0-9_]+)\.([A-Za-z0-9_]+)$/.exec(qualified);
    if (!match) return undefined;
    const handler = modules[match[1]]?.[match[2]];
    if (!handler) return undefined;
    return handler(...(callArgs ?? []));
  };
}
