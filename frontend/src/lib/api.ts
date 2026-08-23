// Typed wrappers over the generated Wails bindings. The Go context
// argument is null from the frontend (Wails injects it server-side).
import * as App from "../../wailsjs/go/desktop/App";
import type { desktop as gen } from "../../wailsjs/go/models";
import type {
  AgentSummary,
  ConfigState,
  ConfigStatus,
  FileNode,
  HistoryMsg,
  KanbanCard,
  ProviderView,
  ReplyRequest,
  SessionMeta,
  SetupRequest,
  SkillDTO,
  TurnStart,
} from "./types";

export const api = {
  version: () => App.Version(),
  configStatus: () => App.ConfigStatus() as Promise<ConfigStatus>,
  providers: () => App.Providers() as Promise<ProviderView[]>,
  configState: () => App.ConfigState() as Promise<ConfigState>,
  saveSetup: (req: SetupRequest) =>
    App.SaveSetup(req as unknown as gen.SetupRequest),
  reload: () => App.Reload(),
  workspace: () => App.Workspace(),
  newChat: () => App.NewChat(),
  listSessions: () => App.ListSessions() as Promise<SessionMeta[]>,
  currentSession: () => App.CurrentSession(),
  resumeSession: (id: string) => App.ResumeSession(id),
  sessionHistory: (id: string) =>
    App.SessionHistory(id) as Promise<HistoryMsg[]>,
  delegationCards: () => App.DelegationCards() as Promise<KanbanCard[]>,
  readFile: (path: string) => App.ReadFile(path),
  fileDiff: (path: string) => App.FileDiff(path),
  getThink: () => App.GetThink(),
  setThink: (level: string) => App.SetThink(level),
  deleteSession: (id: string) => App.DeleteSession(id),
  permissions: () => App.Permissions(),
  allowPermission: (rule: string) => App.AllowPermission(rule),
  denyPermission: (rule: string) => App.DenyPermission(rule),
  skills: () => App.Skills() as Promise<SkillDTO[]>,
  cancelCard: (id: string) => App.CancelCard(id),
  retryCard: (id: string) => App.RetryCard(id),
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
