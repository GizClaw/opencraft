// Typed wrappers over the generated Wails bindings. The Go context
// argument is null from the frontend (Wails injects it server-side).
import * as App from "../../wailsjs/go/desktop/App";
import type { desktop as gen } from "../../wailsjs/go/models";
import type {
  AgentSummary,
  ConfigStatus,
  FileNode,
  ProviderView,
  ReplyRequest,
  SetupRequest,
  TurnStart,
} from "./types";

export const api = {
  version: () => App.Version(),
  configStatus: () => App.ConfigStatus() as Promise<ConfigStatus>,
  providers: () => App.Providers() as Promise<ProviderView[]>,
  saveSetup: (req: SetupRequest) =>
    App.SaveSetup(req as unknown as gen.SetupRequest),
  reload: () => App.Reload(),
  workspace: () => App.Workspace(),
  startTurn: (text: string) => App.StartTurn(text) as Promise<TurnStart>,
  replyPrompt: (promptID: string, reply: ReplyRequest) =>
    App.ReplyPrompt(promptID, reply as unknown as gen.ReplyRequest),
  cancelTurn: (runID: string) => App.CancelTurn(runID),
  listAgents: () => App.ListAgents() as Promise<AgentSummary[]>,
  listDir: (dir: string) => App.ListDir(dir) as Promise<FileNode[]>,
  openPath: (path: string) => App.OpenPath(path),
};
