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
  // fontFamilies is the host font catalogue the appearance picker lists.
  fontFamilies?: string[];
  // NewChat responses consumed in order, then an auto-incrementing id.
  newChatIds?: string[];
  // Per-conversation archive and per-run archive responses used by
  // resume and turn_end reconciliation.
  sessionTurnsByID?: Record<string, unknown[]>;
  turnByRunID?: Record<string, unknown>;
  listSessions?: unknown[];
  listSessionsInWorkspace?: Record<string, unknown[]>;
  // metricPoints overrides the Diagnostics.MetricRange answer; leave it
  // unset for the default of "no samples".
  metricPoints?: Array<{ ts: number; value: number; attrs?: unknown }>;
  workspaces?: unknown[];
  sessionTurns?: unknown[];
  automations?: unknown[];
  // processes is the Session.Processes answer: the sandboxed child
  // processes of the conversation with their output tails. A spec that
  // needs the feed to change over time (a tail that grows, a process
  // that exits, a second process appearing) replaces the handler through
  // window.__ocMockByModule.Session.Processes.
  processes?: unknown[];
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
    // mtime_ns is the stamp the real binding carries; the viewer
    // compares it with Git.FileMarks' to notice an out-of-band write.
    mtime_ns?: number;
  };
  // fileMarks is the Git.FileMarks answer: the viewer's rail chip and
  // gutter read it, so a spec can hand over a changed file without a
  // repository behind it. Left unset the call answers "not a repo".
  fileMarks?: Record<string, unknown>;
  // gitStatus is the Git.Status answer: the file tree's per-file badges
  // and their folder roll-up read the whole-repository snapshot through
  // it. Left unset the workspace answers as a repository with no
  // changes.
  gitStatus?: Record<string, unknown>;
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
  // Steer call log: a mid-turn message leaves the composer as exactly one
  // RPC (Conversation.Steer), so the arguments are the only proof the
  // text went to the live run with that run's id instead of into the Tab
  // queue. Specs read them back from window.__ocSteerCalls.
  const steerCalls: { runID: string; text: string }[] = [];
  // StartTurn call log: "continue" on an interrupted turn leaves the
  // composer as exactly one Conversation.StartTurn, so the request body
  // is the only proof it carried the turn's own message instead of a
  // synthetic prompt.
  const startTurnCalls: { contextID: string; text: string }[] = [];
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
  // The long-term memory card renders its counts from UserMemoryState and
  // its rows from MemoryFacts, so the mock answers a complete state
  // instead of `undefined` (which would render as an empty-looking crash).
  const userMemoryState: Handler = async () => ({
    enabled: true,
    inject_max_items: 12,
    inject_max_chars: 2048,
    min_inject_max_items: 1,
    max_inject_max_items: 50,
    default_inject_max_items: 12,
    min_inject_max_chars: 256,
    max_inject_max_chars: 16384,
    default_inject_max_chars: 2048,
    max_items: 200,
    max_text_bytes: 4096,
    available: true,
    workspace: config.workspace ?? '/workspace',
    live: 0,
    stale: 0,
  });
  // The context card spells out what its two counts add up to and prints
  // the byte budget in a human unit, so the mock answers numbers instead
  // of `{}` (which would render as NaN in both).
  const memoryConfig: Handler = async () => ({
    max_raw_messages: 36,
    preserve_recent: 4,
    max_summary_bytes: 4096,
    replay_full_history: false,
  });
  const reviewState: Handler = async () => ({
    enabled: false,
    every_turns: 5,
    min_tool_calls: 4,
    on_failure: true,
    max_suggestions: 3,
    timeout_seconds: 90,
    min_every_turns: 1,
    max_every_turns: 100,
    default_every_turns: 5,
    min_min_tool_calls: 0,
    max_min_tool_calls: 100,
    default_min_tool_calls: 4,
    min_max_suggestions: 1,
    max_max_suggestions: 10,
    min_timeout_seconds: 15,
    max_timeout_seconds: 600,
    available: true,
    pending: 0,
    accepted: 0,
    discarded: 0,
  });
  const skillLifecycleState: Handler = async () => ({
    enabled: true,
    stale_after_days: 45,
    min_uses: 3,
    usage_window_days: 90,
    min_stale_after_days: 7,
    max_stale_after_days: 365,
    default_stale_after_days: 45,
    min_min_uses: 1,
    max_min_uses: 50,
    default_min_uses: 3,
    min_usage_window_days: 14,
    max_usage_window_days: 365,
    default_usage_window_days: 90,
    skills: [],
    archives: [],
    usage_available: true,
    curator_available: true,
  });
  // The delegation policy card reads its limits, bounds and the
  // registered targets in one state call.
  const delegationState: Handler = async () => ({
    max_concurrency: 4,
    max_depth: 8,
    allowed_targets: [],
    blocked_targets: [],
    min_max_concurrency: 1,
    max_max_concurrency: 16,
    default_max_concurrency: 4,
    min_max_depth: 1,
    max_max_depth: 16,
    default_max_depth: 8,
    max_targets: 64,
    targets: ['assistant'],
    targets_available: true,
  });

  const defaults: Record<string, Record<string, Handler>> = {
    Agent: {
      Detail: async () => null,
      List: emptyList,
      Unregister: noop,
      Update: noop,
    },
    Automation: {
      AutomationSessions: emptyList,
      CancelRun: noop,
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
      MemoryConfig: memoryConfig,
      ModelCatalog: emptyList,
      ModelOptions: emptyList,
      ModelUsage: emptyList,
      ModelUsageSeries: emptyList,
      Providers: emptyList,
      Reload: noop,
      SaveInstances: noop,
      SaveMCP: noop,
      SaveMemory: noop,
      UserMemoryState: userMemoryState,
      MemoryFacts: emptyList,
      AddMemoryFact: noop,
      UpdateMemoryFact: noop,
      RemoveMemoryFact: noop,
      SetMemoryFactStale: noop,
      SaveUserMemorySettings: noop,
      SaveToolOptions: noop,
      TestMCP: noop,
      ToolOptions: async () => ({
        image: { instances: [] },
        video: { instances: [] },
      }),
      WebSearchConfig: async () => ({
        enabled: true,
        provider: 'auto',
        max_results: 8,
        timeout: '15s',
        endpoints: {},
        secrets_available: true,
        providers: [
          {
            id: 'exa',
            keyless: true,
            key_set: false,
            endpoint: 'https://mcp.exa.ai/mcp',
          },
          {
            id: 'parallel',
            keyless: true,
            key_set: false,
            endpoint: 'https://search.parallel.ai/mcp',
          },
          {
            id: 'tavily',
            keyless: false,
            key_set: false,
            endpoint: 'https://api.tavily.com/search',
          },
          {
            id: 'brave',
            keyless: false,
            key_set: false,
            endpoint: 'https://api.search.brave.com/res/v1/web/search',
          },
        ],
      }),
      SaveWebSearch: noop,
      TestWebSearch: async () => ({ provider: 'parallel', results: [] }),
      Version: async () => '0.1.0-test',
    },
    Review: {
      AcceptReviewSuggestion: noop,
      DiscardReviewSuggestion: noop,
      ReviewSettings: reviewState,
      ReviewSuggestions: emptyList,
      SaveReviewSettings: noop,
    },
    SkillLifecycle: {
      PinSkill: noop,
      RestoreSkill: noop,
      RetireSkill: noop,
      SkillArchives: emptyList,
      SkillUsage: skillLifecycleState,
      UnpinSkill: noop,
      SaveSkillLifecycleSettings: noop,
    },
    Delegation: {
      DelegationState: delegationState,
      SaveDelegationSettings: noop,
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
        const request = req as {
          context_id?: string;
          message?: {
            content?: { parts?: Array<{ type?: string; text?: string }> };
          };
        };
        startTurnCalls.push({
          contextID: request?.context_id ?? config.currentSession ?? 's-1',
          text: (request?.message?.content?.parts ?? [])
            .filter((part) => part.type === 'text')
            .map((part) => part.text ?? '')
            .join(''),
        });
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
      // Recorded rather than left to the unknown-binding fallback: a
      // steer that reaches the wrong run — or never leaves the composer
      // — has to fail a spec, and the fallback answers undefined for
      // every name it does not know.
      Steer: async (runID: string, text: string) => {
        steerCalls.push({ runID, text });
      },
    },
    Diagnostics: {
      ClearCaches: async () => ({ dirs: [], bytes: 0 }),
      PerfProbe: async () => false,
      SetPerfProbe: noop,
      CaptureHeapProfile: async () => ({
        path: '/user/diagnostics/heap-test.pprof',
        bytes: 2048,
      }),
      // The diagnostics tab renders the whole environment grid from this
      // report: a partial one leaves every cell blank and prints the raw
      // plural keys where the session counters are missing, so the mock
      // answers with the same shape a real backend returns.
      Diagnostics: async () => ({
        version: '0.1.0-test',
        go_version: 'go1.24.0',
        node_version: 'v24.13.0',
        git_version: 'git version 2.47.1',
        platform: 'darwin',
        arch: 'arm64',
        work_dir: config.workspace ?? '/workspace',
        user_dir: '/user',
        config_valid: true,
        inference_configured: true,
        git_repo: true,
        git_branch: 'main',
        session_count: 12,
        active_runs: 1,
        sandbox_backend: 'seatbelt',
        sandbox_available: true,
        exec_shell: '/bin/zsh -c',
        usage_total_tokens: 128400,
      }),
      EvaluateCommandPolicy: async () => ({ command: '', allowed: true }),
      // The charts only exercise their drawing path with points to plot;
      // the default empty answer keeps every card in its empty state.
      MetricRange: async () =>
        config.metricPoints ??
        Array.from({ length: 900 }, (_, i) => ({
          ts: Date.now() - (900 - i) * 60_000,
          value: 100_000 + ((i * 7919) % 40_000),
          attrs: {},
        })),
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
      // The command-pool card reads and writes desktop.json; a fixed
      // shape is enough for the UI tests.
      ExecPool: async () => ({
        prewarm: 1,
        maxIdle: 4,
        maxActive: 16,
        idleMinutes: 5,
        idle: 1,
        active: 0,
      }),
      SetExecPool: async (
        prewarm: number,
        maxIdle: number,
        maxActive: number,
        idleMinutes: number,
      ) => ({
        prewarm,
        maxIdle,
        maxActive,
        idleMinutes,
        idle: 0,
        active: 0,
      }),
      // The PATH card reads the process environment on mount and the
      // runtime reload is the host's business, so a fixed shape is enough.
      PathEnvironment: async () => ({
        path: '/usr/bin:/bin:/usr/sbin:/sbin',
        segments: [
          { dir: '/usr/bin', source: 'inherited', present: true },
          { dir: '/opt/homebrew/bin', source: 'candidate', present: true },
        ],
        prepend: [],
        rejected: [],
        missing: [],
        reloaded: true,
      }),
      ResolvePath: async () => ({
        path: '/usr/bin:/bin:/usr/sbin:/sbin',
        segments: [
          { dir: '/usr/bin', source: 'inherited', present: true },
          { dir: '/opt/homebrew/bin', source: 'candidate', present: true },
        ],
        prepend: [],
        rejected: [],
        missing: [],
        reloaded: true,
      }),
      SetPathPrepend: async () => ({
        path: '/usr/bin:/bin:/usr/sbin:/sbin',
        segments: [
          { dir: '/usr/bin', source: 'inherited', present: true },
          { dir: '/opt/homebrew/bin', source: 'candidate', present: true },
        ],
        prepend: [],
        rejected: [],
        missing: [],
        reloaded: true,
      }),
      SetTelemetryExport: noop,
      TelemetryExport: async () => ({
        enabled: true,
        configured: false,
        endpoint: '',
        insecure: false,
        headerNames: [],
        owner: '',
      }),
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
              mtime_ns: config.viewerFile.mtime_ns ?? 1,
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
    Git: {
      // The viewer's marks chip and gutter call FileMarks on every open
      // tab; specs that do not configure it see a clean, repo-less
      // answer, which is what the rest of the rail already assumes.
      FileMarks: async () =>
        config.fileMarks ?? { in_repo: false, mtime_ns: 0, size: 0 },
      Status: async () => ({
        root: config.workspace ?? '/workspace',
        workspace: config.workspace ?? '/workspace',
        truncated: false,
        entries: [],
        ...(config.gitStatus ?? {}),
      }),
    },
    Lifecycle: {
      GetCloseToTray: async () => true,
      GetPetsSettings: async () => ({
        enabled: config.petsEnabled ?? true,
        assistantCharacter: config.assistantCharacter ?? 'assistant-default',
      }),
      // the appearance document: specs override GetUISettings to seed a
      // stored font/size, and the panel's writes are recorded so a spec can
      // assert what would have been persisted.
      GetUISettings: async () =>
        (win as { __ocUISettings?: unknown }).__ocUISettings ?? {
          fontFamily: 'system',
          fontFamilyName: '',
          codeFont: 'system',
          codeFontName: '',
          fontScale: 1.12,
        },
      // A small stand-in for the host font catalogue: specs pick from it and
      // override it to cover the "cannot enumerate" path.
      ListFonts: async () =>
        config.fontFamilies ?? ['DejaVu Sans', 'Fira Code', 'PingFang SC'],
      ReportUserActivity: noop,
      RequestClose: noop,
      SetCloseToTray: noop,
      SetUISettings: async (settings: unknown) => {
        const store = win as { __ocUISettingsCalls?: unknown[] };
        const calls =
          store.__ocUISettingsCalls ?? (store.__ocUISettingsCalls = []);
        calls.push(settings);
      },
      SetLanguage: noop,
      SetPetsSettings: noop,
    },
    Pet: {
      Activate: async () => recordPet('Activate', []),
      Activities: emptyList,
      BeginDrag: async () => recordPet('BeginDrag', []),
      EndDrag: async () => recordPet('EndDrag', []),
      Diagnostics: async () =>
        config.petDiagnostics ?? {
          drives: { attention: 60, energy: 40, comfort: 50 },
          mood: 'content',
          stats: { poke_count: 0 },
          disposition: 'idle',
          phase: 'idle',
          walking: false,
          hovered: false,
        },
      ListPacks: async () => config.petPacks ?? [],
      MoveBy: async (dx: number, dy: number) => recordPet('MoveBy', [dx, dy]),
      PackAsset: async (asset: string) => {
        recordPet('PackAsset', [asset]);
        return config.petAsset ?? '';
      },
      Poke: async () => recordPet('Poke', []),
      ReportGeometry: async (geometry: unknown) => {
        recordPet('ReportGeometry', [geometry]);
      },
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
      Processes: async () => config.processes ?? [],
      // Turns mirrors the paged binding: newest `limit` turns older than
      // `beforeSeq` (limit <= 0 keeps the whole fixture). Fixtures without
      // a seq are treated as older history so they survive paging.
      Turns: async (id: string, limit?: number, beforeSeq?: number) => {
        const fixture = (config.sessionTurnsByID?.[id] ??
          config.sessionTurns ??
          []) as Array<{ seq?: number }>;
        let turns = fixture;
        if (typeof beforeSeq === 'number' && beforeSeq > 0) {
          turns = turns.filter(
            (turn) => typeof turn.seq !== 'number' || turn.seq < beforeSeq,
          );
        }
        if (typeof limit === 'number' && limit > 0 && turns.length > limit) {
          turns = turns.slice(turns.length - limit);
        }
        return turns;
      },
      TurnByRunID: async (_id: string, runID: string) => {
        const turn = config.turnByRunID?.[runID];
        if (!turn) {
          throw new Error(`TurnByRunID: archive turn not found for ${runID}`);
        }
        return turn;
      },
    },
    Settings: {
      AllowEscalatedPermission: noop,
      AllowPermission: noop,
      DeleteSkill: noop,
      DenyEscalatedPermission: noop,
      DenyPermission: noop,
      EscalatedPermissions: emptyList,
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
    CancelAutomationRun: ['Automation', 'CancelRun'],
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
    __ocSteerCalls: typeof steerCalls;
    __ocStartTurnCalls: typeof startTurnCalls;
  };
  exposed.__ocMockByModule = modules;
  exposed.__ocPetCalls = petCalls;
  exposed.__ocPetReports = petReports;
  exposed.__ocSteerCalls = steerCalls;
  exposed.__ocStartTurnCalls = startTurnCalls;
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
