// The Go bindings are one module object shared by every store test file.
//
// `lib/api` is mocked per test file (vitest's registry is per module
// graph), and each of those files imports the mock table from here: the
// factory in the test file resolves this same module instance, so the
// assertions and the mocked call agree. Keeping the table in one place
// means a binding added to `lib/api` is added here once instead of in
// every file that drives the store.
import { vi } from 'vitest';

export const apiMock = {
  profile: vi.fn(),
  configStatus: vi.fn(),
  workspace: vi.fn(),
  sessionMode: vi.fn(),
  currentSession: vi.fn(),
  resumeSession: vi.fn(),
  getThink: vi.fn(),
  getModel: vi.fn(),
  modelOptions: vi.fn(),
  sessionDefaults: vi.fn(),
  saveSessionDefaults: vi.fn(),
  uiSettings: vi.fn(),
  listSessions: vi.fn(),
  sessionTurns: vi.fn(),
  turnsSince: vi.fn(),
  turnByRunID: vi.fn(),
  loadWorkspaces: vi.fn(),
  loadAutomations: vi.fn(),
  automations: vi.fn(),
  automationRuns: vi.fn(),
  openWorkspace: vi.fn(),
  workspaces: vi.fn(),
  setSessionMode: vi.fn(),
  setThink: vi.fn(),
  setModel: vi.fn(),
  newChat: vi.fn(),
  startTurn: vi.fn(),
  steerTurn: vi.fn(),
  forkTurn: vi.fn(),
  cancelTurn: vi.fn(),
  deleteSession: vi.fn(),
  replyPrompt: vi.fn(),
  resolveTarget: vi.fn(),
  openExternal: vi.fn(),
};

// The frontend error bridge is the only place a missed switch leaves a
// trace: every caller turns the failure into a flash the reader sees
// once, and the click path catches it before it can reach the log.
export const diagnosticsMock = {
  ReportFrontendError: vi.fn(
    async (_source: string, _message: string, _stack: string) => {},
  ),
};
