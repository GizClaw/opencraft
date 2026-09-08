// Typed wrappers over the desktopv2 Wails bindings.
import i18n from '../i18n';
import * as Agent from '../../wailsjs/go/bindings/Agent';
import * as Automation from '../../wailsjs/go/bindings/Automation';
import * as Config from '../../wailsjs/go/bindings/Config';
import * as Conversation from '../../wailsjs/go/bindings/Conversation';
import * as Diagnostics from '../../wailsjs/go/bindings/Diagnostics';
import * as File from '../../wailsjs/go/bindings/File';
import * as Git from '../../wailsjs/go/bindings/Git';
import * as Lifecycle from '../../wailsjs/go/bindings/Lifecycle';
import * as Plugin from '../../wailsjs/go/bindings/Plugin';
import * as PullRequests from '../../wailsjs/go/bindings/PullRequests';
import * as Secret from '../../wailsjs/go/bindings/Secret';
import * as Session from '../../wailsjs/go/bindings/Session';
import * as Settings from '../../wailsjs/go/bindings/Settings';
import * as Workspace from '../../wailsjs/go/bindings/Workspace';
import type {
  bindings as gen,
  config as genConfig,
} from '../../wailsjs/go/models';
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
  GitDiff,
  GitHubPRDetail,
  GitHubPull,
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
  gitRepo: () => Git.Repo() as unknown as Promise<GitRepo>,
  gitStatus: () => Git.Status() as unknown as Promise<GitStatus>,
  gitLog: (limit: number) =>
    Git.Log(limit) as unknown as Promise<GitLogEntry[]>,
  gitBranches: () => Git.Branches() as unknown as Promise<GitBranch[]>,
  gitDiff: (path: string, cached: boolean) =>
    Git.Diff(path, cached) as unknown as Promise<GitDiff>,
  gitStage: (paths: string[]) => Git.Stage(paths) as unknown as Promise<string>,
  gitUnstage: (paths: string[]) =>
    Git.Unstage(paths) as unknown as Promise<string>,
  gitCommit: (message: string) =>
    Git.Commit(message) as unknown as Promise<string>,
  gitCheckout: (branch: string) =>
    Git.Checkout(branch) as unknown as Promise<string>,
  gitNewBranch: (name: string) =>
    Git.NewBranch(name) as unknown as Promise<string>,
  gitDiscard: (paths: string[], staged: boolean) =>
    Git.Discard(paths, staged) as unknown as Promise<string>,
  gitClean: (paths: string[]) => Git.Clean(paths) as unknown as Promise<string>,
  gitPull: () => Git.Pull() as unknown as Promise<string>,
  gitPush: (force: boolean) => Git.Push(force) as unknown as Promise<string>,
  gitHubAvailable: () =>
    PullRequests.Availability() as unknown as Promise<PRAvailability>,
  gitHubPRList: () => PullRequests.List() as unknown as Promise<GitHubPull[]>,
  gitHubPRDetail: (number: number) =>
    PullRequests.Detail(number) as unknown as Promise<GitHubPRDetail>,
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
  modelUsageSeries: (
    model: string,
    granularity: 'hour' | 'day',
    utcOffsetMinutes: number,
    start: string,
    end: string,
  ) =>
    Config.ModelUsageSeries(model, granularity, utcOffsetMinutes, start, end),
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
  permissions: () => Settings.Permissions(),
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
  pluginList: () => Plugin.List() as Promise<PluginSummary[]>,
  pluginTools: (id: string) =>
    Plugin.Tools(id) as unknown as Promise<PluginToolDTO[]>,
  pluginSkills: (id: string) =>
    Plugin.Skills(id) as unknown as Promise<SkillDTO[]>,
  pluginBundle: (id: string) => Plugin.Bundle(id),
  pluginInstall: (dir: string) => Plugin.Install(dir),
  pluginInstallZip: (zip: string) => Plugin.InstallZip(zip),
  pluginInspect: (path: string) => Plugin.Inspect(path),
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
