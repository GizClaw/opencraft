// Typed wrappers over the Wails v3 desktop services.
import i18n from '../i18n';
import * as Agent from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/agent';
import * as Automation from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/automation';
import * as Config from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/config';
import * as Conversation from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/conversation';
import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';
import * as File from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/file';
import * as Git from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/git';
import * as Lifecycle from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/lifecycle';
import * as Plugin from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/plugin';
import * as Pet from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/pet';
import * as PullRequests from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/pullrequests';
import * as Secret from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/secret';
import * as Session from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/session';
import * as Settings from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/settings';
import * as Workspace from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/workspace';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';
import type * as genConfig from '../../bindings/github.com/GizClaw/opencraft/internal/foundation/config/models';
import type {
  ActiveRunDTO,
  AgentSummary,
  AgentDetail,
  AutomationRun,
  AutomationTask,
  AttachmentDTO,
  CacheClearResult,
  ConfigState,
  ConfigStatus,
  DiagnosticsReport,
  FilePreview,
  FileNode,
  GitBranch,
  GitChange,
  GitChangeKind,
  GitCommitFiles,
  GitDiff,
  GitHubCheck,
  GitHubPRDetail,
  GitHubPull,
  GitHubReviewThread,
  GitHubTimelineItem,
  GitLogEntry,
  GitRepo,
  GitStatus,
  PRAvailability,
  ResolvedTarget,
  HistoryMessage,
  InferenceRequest,
  MCPServer,
  MCPStatus,
  MemorySettings,
  ModelUsageStat,
  ModelOption,
  ProviderModelCatalog,
  PatchFileDTO,
  PolicyDecision,
  PetsSettings,
  ProviderView,
  ReplyRequest,
  SandboxProbeResult,
  SearchFileHit,
  SessionDefaults,
  SessionMeta,
  SessionSnapshot,
  SessionImportDTO,
  SessionTurn,
  SkillDTO,
  TurnStart,
  TurnMessage,
  WorkspaceMeta,
} from './types';
import type {
  PluginKVEntry,
  PluginSummary,
  PluginToolDTO,
} from '../plugins/types';
import type { PetPack } from '../pet/pack';
import type { PetActivityDTO } from '../pet/state';
import type { PetMindDebug } from '../pet/state';
import type * as genPlugin from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/plugins/models';
import type * as genPet from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/pet/models';

function pluginSummaryOf(p: genPlugin.PluginSummary): PluginSummary {
  return {
    ...p,
    permissions: p.permissions ?? [],
    panels: p.panels ?? [],
    entries: p.entries ?? [],
  };
}

// ---- Git/PR DTO adapters ----
//
// The Wails-generated models carry the exact Go DTO shapes, but their
// enum-ish fields are plain strings. These adapters narrow those
// strings to the domain unions in lib/types without `as unknown as`,
// so a Go-side shape change fails the TypeScript check in exactly one
// place instead of silencing every call site.

function gitChangeKindOf(kind: string): GitChangeKind {
  switch (kind) {
    case 'modified':
    case 'added':
    case 'deleted':
    case 'renamed':
    case 'copied':
    case 'typechange':
    case 'untracked':
    case 'unmerged':
      return kind;
    default:
      // A future git status code degrades to the modified marker
      // instead of rendering an unknown glyph.
      return 'modified';
  }
}

function gitChangeOf(dto: gen.GitChangeDTO): GitChange {
  return { ...dto, kind: gitChangeKindOf(dto.kind) };
}

function toGitStatus(dto: gen.GitStatusDTO): GitStatus {
  return {
    root: dto.root,
    workspace: dto.workspace,
    branch: dto.branch,
    truncated: dto.truncated,
    entries: (dto.entries ?? []).map(gitChangeOf),
  };
}

function toGitCommitFiles(dto: gen.GitCommitFilesDTO): GitCommitFiles {
  return {
    truncated: dto.truncated,
    files: (dto.files ?? []).map((f) => ({
      ...f,
      kind: gitChangeKindOf(f.kind),
    })),
  };
}

function pullStateOf(state: string): GitHubPull['state'] {
  switch (state) {
    case 'open':
    case 'closed':
    case 'merged':
      return state;
    default:
      // Unknown states render as closed (non-actionable) rather than
      // being surfaced as an open pull request.
      return 'closed';
  }
}

function checkKindOf(kind: string): GitHubCheck['kind'] {
  return kind === 'status' ? 'status' : 'check_run';
}

function timelineKindOf(kind: string): GitHubTimelineItem['kind'] {
  return kind === 'review' ? 'review' : 'comment';
}

function threadSideOf(side: string | undefined): GitHubReviewThread['side'] {
  return side === 'LEFT' || side === 'RIGHT' ? side : undefined;
}

export const api = {
  version: () => Config.Version(),
  profile: () => Config.Profile(),
  configStatus: () => Config.ConfigStatus() as unknown as Promise<ConfigStatus>,
  providers: () => Config.Providers() as unknown as Promise<ProviderView[]>,
  configState: () => Config.ConfigState() as Promise<ConfigState>,
  modelCatalog: () => Config.ModelCatalog() as Promise<ProviderModelCatalog[]>,
  saveInstances: (req: InferenceRequest) =>
    Config.SaveInstances(req as unknown as gen.InferenceRequest),
  reload: () => Config.Reload(),
  workspace: () => Workspace.Active(),
  newChat: async () => {
    // The binding reports the values actually applied when the
    // conversation was minted, so the UI snapshot can never drift
    // from the backend state.
    const minted = await Conversation.NewChat();
    return {
      session_id: minted.session_id,
      mode: minted.mode,
      think: minted.think,
      model: minted.model,
    } as SessionSnapshot;
  },
  forkTurn: (contextID: string, runID: string) =>
    Conversation.ForkTurn(contextID, runID),
  listSessions: () => Session.List() as unknown as Promise<SessionMeta[]>,
  listSessionsInWorkspace: (workspace: string) =>
    Session.ListInWorkspace(workspace) as unknown as Promise<SessionMeta[]>,
  currentSession: () => Conversation.CurrentSession(),
  activeRun: async (id: string) =>
    ({ run_id: await Session.ActiveRun(id) }) as ActiveRunDTO,
  resumeSession: async (id: string) => {
    await Conversation.ResumeSession(id);
    return {
      session_id: id,
      mode: await Conversation.SessionMode(),
      think: await Settings.GetThink(),
      model: await Settings.GetModel(),
    } as SessionSnapshot;
  },
  sessionHistory: (id: string) =>
    Session.History(id, -1) as unknown as Promise<HistoryMessage[]>,
  sessionTurns: (id: string) =>
    Session.Turns(id) as unknown as Promise<SessionTurn[]>,
  turnByRunID: (conversationID: string, runID: string) =>
    Session.TurnByRunID(
      conversationID,
      runID,
    ) as unknown as Promise<SessionTurn>,
  workspaces: () => Workspace.List() as Promise<WorkspaceMeta[]>,
  openWorkspace: (path: string) => Workspace.Open(path),
  removeWorkspace: (id: string) => Workspace.Remove(id),
  fileDiff: (path: string) => File.Diff(path),
  gitRepo: (): Promise<GitRepo> => Git.Repo(),
  gitStatus: async (): Promise<GitStatus> => toGitStatus(await Git.Status()),
  gitLog: async (limit: number): Promise<GitLogEntry[]> =>
    (await Git.Log(limit)) ?? [],
  gitBranches: async (): Promise<GitBranch[]> => (await Git.Branches()) ?? [],
  gitDiff: async (path: string, cached: boolean): Promise<GitDiff> =>
    Git.Diff(path, cached),
  gitCommitFiles: async (oid: string): Promise<GitCommitFiles> =>
    toGitCommitFiles(await Git.CommitFiles(oid)),
  gitCommitDiff: async (oid: string, path: string): Promise<GitDiff> =>
    Git.CommitDiff(oid, path),
  gitStage: (paths: string[]) => Git.Stage(paths),
  gitUnstage: (paths: string[]) => Git.Unstage(paths),
  gitCommit: (message: string) => Git.Commit(message),
  gitCheckout: (branch: string) => Git.Checkout(branch),
  gitNewBranch: (name: string) => Git.NewBranch(name),
  gitDiscard: (paths: string[], staged: boolean) => Git.Discard(paths, staged),
  gitClean: (paths: string[]) => Git.Clean(paths),
  gitPull: () => Git.Pull(),
  gitPush: (force: boolean) => Git.Push(force),
  gitHubAvailable: async (): Promise<PRAvailability> =>
    PullRequests.Availability(),
  gitHubPRList: async (): Promise<GitHubPull[]> =>
    ((await PullRequests.List()) ?? []).map((p) => ({
      ...p,
      state: pullStateOf(p.state),
    })),
  gitHubPRDetail: async (number: number): Promise<GitHubPRDetail> => {
    const d = await PullRequests.Detail(number);
    return {
      ...d,
      state: pullStateOf(d.state),
      commits: d.commits ?? [],
      checks: (d.checks ?? []).map((c) => ({
        ...c,
        kind: checkKindOf(c.kind),
      })),
      conversation: (d.conversation ?? []).map((item) => ({
        ...item,
        kind: timelineKindOf(item.kind),
      })),
      threads: (d.threads ?? []).map((t) => ({
        ...t,
        side: threadSideOf(t.side),
        comments: t.comments ?? [],
      })),
    };
  },
  getThink: () => Settings.GetThink(),
  setThink: (level: string) => Settings.SetThink(level),
  getModel: () => Settings.GetModel(),
  setModel: (model: string) => Settings.SetModel(model),
  sessionDefaults: () =>
    Settings.GetSessionDefaults() as unknown as Promise<SessionDefaults>,
  saveSessionDefaults: (d: SessionDefaults) =>
    Settings.SetSessionDefaults(d as unknown as gen.SessionDefaults),
  modelOptions: () =>
    Config.ModelOptions() as unknown as Promise<ModelOption[]>,
  modelUsage: () => Config.ModelUsage() as unknown as Promise<ModelUsageStat[]>,
  modelUsageSessionCount: () => Config.ModelUsageSessionCount(),
  modelUsageSeries: async (
    model: string,
    granularity: 'hour' | 'day',
    utcOffsetMinutes: number,
    start: string,
    end: string,
  ) =>
    (await Config.ModelUsageSeries(
      model,
      granularity,
      utcOffsetMinutes,
      start,
      end,
    )) ?? [],
  mcpConfig: () => Config.MCPConfig() as Promise<MCPServer[]>,
  saveMCP: (servers: MCPServer[]) =>
    Config.SaveMCP(servers as unknown as genConfig.MCPServer[]),
  agentDetail: (name: string) =>
    Agent.Detail(name) as unknown as Promise<AgentDetail>,
  updateAgent: (name: string, description: string, graph: string) =>
    Agent.Update(name, description, graph),
  mcpStatus: () => Config.MCPStatus() as unknown as Promise<MCPStatus[]>,
  testMCP: (server: MCPServer) =>
    Config.TestMCP(server as unknown as genConfig.MCPServer),
  deleteSession: async (id: string) => {
    const res = await Session.Delete(id);
    return {
      session_id: res.session_id ?? '',
      mode: res.mode ?? '',
      think: res.think ?? '',
      model: res.model ?? '',
    } as SessionSnapshot;
  },
  permissions: async () => (await Settings.Permissions()) ?? [],
  allowPermission: (rule: string) => Settings.AllowPermission(rule),
  denyPermission: (rule: string) => Settings.DenyPermission(rule),
  skills: () => Settings.Skills() as unknown as Promise<SkillDTO[]>,
  skillContent: (path: string) => Settings.SkillContent(path),
  deleteSkill: (path: string) => Settings.DeleteSkill(path),
  installSkill: (repo: string, scope: string, subpath: string) =>
    Settings.InstallSkill(repo, scope, subpath),
  renderPatch: (patch: string) =>
    File.RenderPatch(patch) as unknown as Promise<PatchFileDTO[]>,
  renderSkillPatch: (name: string, scope: string, patch: string) =>
    Settings.RenderSkillPatch(name, scope, patch) as unknown as Promise<
      PatchFileDTO[]
    >,
  memoryConfig: () => Config.MemoryConfig() as Promise<MemorySettings>,
  saveMemory: (s: MemorySettings) =>
    Config.SaveMemory(s as unknown as genConfig.MemorySettings),
  diagnostics: () =>
    Diagnostics.Diagnostics() as unknown as Promise<DiagnosticsReport>,
  runSandboxProbe: () =>
    Diagnostics.RunSandboxProbe() as unknown as Promise<SandboxProbeResult>,
  evaluateCommandPolicy: (command: string) =>
    Diagnostics.EvaluateCommandPolicy(
      command,
    ) as unknown as Promise<PolicyDecision>,
  clearCaches: () =>
    Diagnostics.ClearCaches() as unknown as Promise<CacheClearResult>,
  chooseWorkspace: () =>
    Workspace.ChooseWorkspace(i18n.t('sidebar.chooseWorkspaceTitle')),
  pluginList: async () => ((await Plugin.List()) ?? []).map(pluginSummaryOf),
  pluginTools: (id: string) =>
    Plugin.Tools(id) as unknown as Promise<PluginToolDTO[]>,
  pluginSkills: (id: string) =>
    Plugin.Skills(id) as unknown as Promise<SkillDTO[]>,
  pluginBundle: (id: string) => Plugin.Bundle(id),
  pluginInstall: (dir: string) => Plugin.Install(dir),
  pluginInstallZip: (zip: string) => Plugin.InstallZip(zip),
  pluginInspect: async (path: string) =>
    pluginSummaryOf(await Plugin.Inspect(path)),
  pluginUpdate: (id: string, dir: string) => Plugin.Update(id, dir),
  pluginUpdateZip: (id: string, zip: string) => Plugin.UpdateZip(id, zip),
  pluginRollback: (id: string) => Plugin.Rollback(id),
  pluginCheckUpdate: (id: string) => Plugin.CheckUpdate(id),
  pluginApplyUpdate: (id: string) => Plugin.ApplyUpdate(id),
  pluginSetEnabled: (id: string, enabled: boolean) =>
    Plugin.SetEnabled(id, enabled),
  pluginUninstall: (id: string) => Plugin.Uninstall(id),
  pluginInvoke: (id: string, method: string, args: string) =>
    Plugin.Invoke(id, method, args),
  getCloseToTray: () => Lifecycle.GetCloseToTray(),
  setCloseToTray: (closeToTray: boolean) =>
    Lifecycle.SetCloseToTray(closeToTray),
  petSettings: () =>
    Lifecycle.GetPetsSettings() as unknown as Promise<PetsSettings>,
  setPetSettings: (settings: PetsSettings) =>
    Lifecycle.SetPetsSettings(settings as unknown as gen.PetsSettings),
  petListPacks: () => Pet.ListPacks() as unknown as Promise<PetPack[]>,
  petActivities: () => Pet.Activities() as unknown as Promise<PetActivityDTO[]>,
  petDiagnostics: () => Pet.Diagnostics() as unknown as Promise<PetMindDebug>,
  petMoveBy: (dx: number, dy: number) => Pet.MoveBy(dx, dy),
  petSetPosition: (x: number, y: number) => Pet.SetPosition(x, y),
  petActivate: () => Pet.Activate(),
  petPoke: () => Pet.Poke(),
  petSetRoamingPaused: (paused: boolean) => Pet.SetRoamingPaused(paused),
  petGetPosition: () => Pet.Position(),
  reportUserActivity: () => Lifecycle.ReportUserActivity(),
  petPackAsset: (asset: string) => Pet.PackAsset(asset),
  petRegisterPack: (pack: PetPack) =>
    Pet.RegisterPack(pack as unknown as genPet.Pack),
  petUnregisterPack: (id: string) => Pet.UnregisterPack(id),
  closeRequested: () => Lifecycle.RequestClose(),
  pickFolder: (title: string) => File.PickFolder(title),
  pickFile: (title: string, pattern: string) => File.PickFile(title, pattern),
  pluginKVGet: (id: string, key: string) =>
    Plugin.KVGet(id, key) as unknown as Promise<PluginKVEntry>,
  pluginKVList: (id: string) =>
    Plugin.KVList(id) as unknown as Promise<PluginKVEntry[]>,
  pluginKVSet: (id: string, key: string, value: string) =>
    Plugin.KVSet(id, key, value),
  pluginKVDelete: (id: string, key: string) => Plugin.KVDelete(id, key),
  automations: () => Automation.List() as unknown as Promise<AutomationTask[]>,
  saveAutomation: (task: AutomationTask) =>
    Automation.Save(
      task as unknown as gen.AutomationTaskDTO,
    ) as unknown as Promise<AutomationTask>,
  setLanguage: (language: string) => Lifecycle.SetLanguage(language),
  deleteAutomation: (id: string) => Automation.Delete(id),
  runAutomationNow: (id: string) => Automation.RunNow(id),
  automationRuns: (taskId: string) =>
    Automation.Runs(taskId) as unknown as Promise<AutomationRun[]>,
  automationSessions: (workspace: string) =>
    Automation.AutomationSessions(workspace) as unknown as Promise<
      SessionMeta[]
    >,
  secretExists: (scope: string, name: string) => Secret.Exists(scope, name),
  secretDelete: (scope: string, name: string) => Secret.Delete(scope, name),
  readLog: (n: number) => Settings.ReadLog(n),
  renameSession: (id: string, title: string) => Session.Rename(id, title),
  exportSession: (id: string) => Session.ExportMarkdown(id),
  exportSessionBundle: (id: string) => Session.ExportBundle(id),
  importSession: (path: string) =>
    Session.ImportBundle(path) as unknown as Promise<SessionImportDTO>,
  sessionMode: () => Conversation.SessionMode(),
  setSessionMode: (mode: string) => Conversation.SetSessionMode(mode),
  startTurn: (contextID: string, msg: TurnMessage) =>
    Conversation.StartTurn({
      context_id: contextID,
      message: msg,
    } as unknown as gen.StartTurnRequest) as unknown as Promise<TurnStart>,
  readAttachment: (path: string) =>
    File.ReadAttachment(path) as unknown as Promise<AttachmentDTO>,
  importPastedImage: (name: string, dataURL: string) =>
    File.ImportPastedImage(name, dataURL) as unknown as Promise<AttachmentDTO>,
  replyPrompt: (promptID: string, reply: ReplyRequest) =>
    Conversation.ReplyPrompt(
      promptID,
      reply.text ?? '',
      reply.option ?? '',
      reply.options ?? [],
      !!reply.cancel,
    ),
  cancelTurn: (runID: string) => Conversation.CancelTurn(runID),
  listAgents: () => Agent.List() as unknown as Promise<AgentSummary[]>,
  unregisterAgent: (name: string) => Agent.Unregister(name),
  listDir: (dir: string) => File.List(dir) as unknown as Promise<FileNode[]>,
  searchFiles: (query: string, limit?: number) =>
    File.Search(query, limit ?? 50) as unknown as Promise<SearchFileHit[]>,
  resolveTarget: (target: string, base?: string) =>
    File.ResolveTarget(
      target,
      base ?? '',
    ) as unknown as Promise<ResolvedTarget>,
  readPreview: (path: string) =>
    File.ReadPreview(path) as unknown as Promise<FilePreview>,
  openPath: (path: string) => File.OpenPath(path),
  saveArtifactAs: (path: string) => File.SaveArtifactAs(path),
  revealArtifact: (path: string) => File.Reveal(path),
  openArtifactWith: (path: string) => File.OpenArtifactWith(path),
  openExternal: (url: string) => File.OpenExternal(url),
};
