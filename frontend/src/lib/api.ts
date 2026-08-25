// Typed wrappers over the generated Wails bindings. The Go context
// argument is null from the frontend (Wails injects it server-side).
import * as App from "../../wailsjs/go/desktop/App";
import type {
  config as genConfig,
  desktop as gen,
} from "../../wailsjs/go/models";
import type {
  AgentSummary,
  ConfigState,
  ConfigStatus,
  FileNode,
  HistoryMessage,
  KanbanCard,
  InferenceRequest,
  MCPServer,
  MCPStatus,
  ModelUsageStat,
  ModelOption,
  PatchFileDTO,
  ProviderView,
  ReplyRequest,
  SessionMeta,
  ProviderInstance,
  SkillDTO,
  TurnStart,
  UsagePoint,
  WorkspaceMeta,
} from "./types";

export const api = {
  version: () => App.Version(),
  configStatus: () => App.ConfigStatus() as Promise<ConfigStatus>,
  providers: () => App.Providers() as Promise<ProviderView[]>,
  configState: () => App.ConfigState() as Promise<ConfigState>,
  saveInstances: (req: InferenceRequest) =>
    App.SaveInstances(req as unknown as gen.InferenceRequest),
  reload: () => App.Reload(),
  workspace: () => App.Workspace(),
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
    granularity: "hour" | "day",
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
  cancelCard: (id: string) => App.CancelCard(id),
  retryCard: (id: string) => App.RetryCard(id),
  chooseWorkspace: () => App.ChooseWorkspace(),
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
  listDir: (dir: string) => App.ListDir(dir) as Promise<FileNode[]>,
  openPath: (path: string) => App.OpenPath(path),
};
