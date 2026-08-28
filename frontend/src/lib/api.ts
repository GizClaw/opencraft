// Typed wrappers over the generated Wails bindings. The Go context
// argument is null from the frontend (Wails injects it server-side).
import * as App from '../../wailsjs/go/desktop/App';
import type {
  config as genConfig,
  desktop as gen,
} from '../../wailsjs/go/models';
import type {
  AgentSummary,
  AgentDetail,
  AgentUpdateResult,
  CacheClearResult,
  ConfigState,
  ConfigStatus,
  DiagnosticsReport,
  FileNode,
  HistoryMessage,
  KanbanCard,
  InferenceRequest,
  MCPServer,
  MCPStatus,
  MemorySettings,
  ModelUsageStat,
  ModelOption,
  PatchFileDTO,
  PolicyDecision,
  ProviderView,
  ReplyRequest,
  SandboxProbeResult,
  SearchFileHit,
  SessionMeta,
  ProviderInstance,
  ProjectConfigStatus,
  SkillDTO,
  TurnStart,
  UndoState,
  UsagePoint,
  WorkspaceMeta,
} from './types';
import type {
  AuthBeginResult,
  AuthPollResult,
  AuthStatusResult,
  AuthUser,
  PluginKVEntry,
  PluginSummary,
} from '../plugins/types';

export const api = {
  version: () => App.Version(),
  configStatus: () => App.ConfigStatus() as Promise<ConfigStatus>,
  providers: () => App.Providers() as Promise<ProviderView[]>,
  configState: () => App.ConfigState() as Promise<ConfigState>,
  saveInstances: (req: InferenceRequest) =>
    App.SaveInstances(req as unknown as gen.InferenceRequest),
  reload: () => App.Reload(),
  workspace: () => App.Workspace(),
  projectConfigStatus: () =>
    App.ProjectConfigStatus() as Promise<ProjectConfigStatus>,
  setProjectTrust: (path: string, trusted: boolean) =>
    App.SetProjectTrust(path, trusted),
  newChat: () => App.NewChat(),
  listSessions: () => App.ListSessions() as Promise<SessionMeta[]>,
  currentSession: () => App.CurrentSession(),
  resumeSession: (id: string) => App.ResumeSession(id),
  sessionHistory: (id: string) =>
    App.SessionHistory(id) as Promise<HistoryMessage[]>,
  workspaces: () => App.Workspaces() as Promise<WorkspaceMeta[]>,
  openWorkspace: (path: string) => App.OpenWorkspace(path),
  removeWorkspace: (id: string) => App.RemoveWorkspace(id),
  delegationCards: () => App.DelegationCards() as Promise<KanbanCard[]>,
  readFile: (path: string) => App.ReadFile(path),
  fileDiff: (path: string) => App.FileDiff(path),
  getThink: () => App.GetThink(),
  setThink: (level: string) => App.SetThink(level),
  getModel: () => App.GetModel(),
  setModel: (model: string) => App.SetModel(model),
  modelOptions: () => App.ModelOptions() as Promise<ModelOption[]>,
  modelUsage: () => App.ModelUsage() as Promise<ModelUsageStat[]>,
  modelUsageSeries: (
    model: string,
    granularity: 'hour' | 'day',
    utcOffsetMinutes: number,
    start: string,
    end: string,
  ) =>
    App.ModelUsageSeries(
      model,
      granularity,
      utcOffsetMinutes,
      start,
      end,
    ) as Promise<UsagePoint[]>,
  mcpConfig: () => App.MCPConfig() as Promise<MCPServer[]>,
  saveMCP: (servers: MCPServer[]) =>
    App.SaveMCP(servers as unknown as genConfig.MCPServer[]),
  agentDetail: (name: string) =>
    App.AgentDetail(name) as unknown as Promise<AgentDetail>,
  updateAgent: (name: string, description: string, graph: string) =>
    App.UpdateAgent(
      name,
      description,
      graph,
    ) as unknown as Promise<AgentUpdateResult>,
  mcpStatus: () => App.MCPStatus() as Promise<MCPStatus[]>,
  testMCP: (server: MCPServer) =>
    App.TestMCP(server as unknown as genConfig.MCPServer),
  deleteSession: (id: string) => App.DeleteSession(id),
  permissions: () => App.Permissions(),
  allowPermission: (rule: string) => App.AllowPermission(rule),
  denyPermission: (rule: string) => App.DenyPermission(rule),
  skills: () => App.Skills() as Promise<SkillDTO[]>,
  deleteSkill: (path: string) => App.DeleteSkill(path),
  installSkill: (repo: string, scope: string, subpath: string) =>
    App.InstallSkill(repo, scope, subpath) as Promise<string>,
  renderPatch: (patch: string) =>
    App.RenderPatch(patch) as Promise<PatchFileDTO[]>,
  renderSkillPatch: (name: string, scope: string, patch: string) =>
    App.RenderSkillPatch(name, scope, patch) as Promise<PatchFileDTO[]>,
  undoChange: () => App.UndoChange() as Promise<string[]>,
  redoChange: () => App.RedoChange() as Promise<string[]>,
  undoState: () => App.UndoState() as Promise<UndoState>,
  memoryConfig: () => App.MemoryConfig() as Promise<MemorySettings>,
  saveMemory: (s: MemorySettings) =>
    App.SaveMemory(s as unknown as genConfig.MemorySettings) as Promise<void>,
  diagnostics: () => App.Diagnostics() as Promise<DiagnosticsReport>,
  runSandboxProbe: () => App.RunSandboxProbe() as Promise<SandboxProbeResult>,
  evaluateCommandPolicy: (command: string) =>
    App.EvaluateCommandPolicy(command) as Promise<PolicyDecision>,
  clearCaches: () => App.ClearCaches() as Promise<CacheClearResult>,
  cancelCard: (id: string) => App.CancelCard(id),
  chooseWorkspace: () => App.ChooseWorkspace(),
  pluginList: () => App.PluginList() as Promise<PluginSummary[]>,
  pluginBundle: (id: string) => App.PluginBundle(id) as Promise<string>,
  pluginInstall: (dir: string) =>
    App.PluginInstall(dir) as Promise<PluginSummary>,
  pluginSetEnabled: (id: string, enabled: boolean) =>
    App.PluginSetEnabled(id, enabled),
  pluginUninstall: (id: string) => App.PluginUninstall(id),
  pickFolder: (title: string) => App.PickFolder(title) as Promise<string>,
  pluginKVGet: (id: string, key: string) =>
    App.PluginKVGet(id, key) as Promise<PluginKVEntry>,
  pluginKVList: (id: string) =>
    App.PluginKVList(id) as Promise<PluginKVEntry[]>,
  pluginKVSet: (id: string, key: string, value: string) =>
    App.PluginKVSet(id, key, value),
  pluginKVDelete: (id: string, key: string) =>
    App.PluginKVDelete(id, key),
  secretExists: (scope: string, name: string) =>
    App.SecretExists(scope, name) as Promise<boolean>,
  secretDelete: (scope: string, name: string) =>
    App.SecretDelete(scope, name),
  authBegin: (provider: string, clientID: string) =>
    App.AuthBegin(provider, clientID) as Promise<AuthBeginResult>,
  authPoll: (provider: string) =>
    App.AuthPoll(provider) as Promise<AuthPollResult>,
  authRotate: (provider: string) => App.AuthRotate(provider),
  authRevoke: (provider: string) => App.AuthRevoke(provider),
  authStatus: (provider: string) =>
    App.AuthStatus(provider) as Promise<AuthStatusResult>,
  authMe: (provider: string) => App.AuthMe(provider) as Promise<AuthUser>,
  authModels: (provider: string) =>
    App.AuthModels(provider) as Promise<string[]>,
  upsertGatewayProfile: (providerID: string, displayName: string) =>
    App.UpsertGatewayProfile(providerID, displayName),
  removeGatewayProfile: (providerID: string) =>
    App.RemoveGatewayProfile(providerID),
  readLog: (n: number) => App.ReadLog(n),
  renameSession: (id: string, title: string) => App.RenameSession(id, title),
  exportSession: (id: string) => App.ExportSession(id),
  sessionMode: () => App.SessionMode(),
  setSessionMode: (mode: string) => App.SetSessionMode(mode),
  startTurn: (text: string) => App.StartTurn(text) as Promise<TurnStart>,
  replyPrompt: (promptID: string, reply: ReplyRequest) =>
    App.ReplyPrompt(promptID, reply as unknown as gen.ReplyRequest),
  cancelTurn: (runID: string) => App.CancelTurn(runID),
  listAgents: () => App.ListAgents() as Promise<AgentSummary[]>,
  unregisterAgent: (name: string) => App.UnregisterAgent(name),
  conversationDelegationCards: (contextID: string) =>
    App.ConversationDelegationCards(contextID) as Promise<KanbanCard[]>,
  listDir: (dir: string) => App.ListDir(dir) as Promise<FileNode[]>,
  // Fall back to a root-level listing when the running binary predates
  // the SearchFiles binding, so "@" never breaks into a blank popup.
  searchFiles: async (query: string, limit?: number) => {
    const cap = limit ?? 50;
    if (typeof App.SearchFiles !== 'function') {
      const wd = await App.Workspace();
      const nodes = await App.ListDir(wd);
      const q = query.toLowerCase();
      return nodes
        .filter((n) => n.name.toLowerCase().includes(q))
        .slice(0, cap)
        .map((n) => ({ path: n.name, is_dir: n.is_dir }));
    }
    return App.SearchFiles(query, cap) as Promise<SearchFileHit[]>;
  },
  openPath: (path: string) => App.OpenPath(path),
  openExternal: (url: string) => App.OpenExternal(url),
};
