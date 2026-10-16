import { UIEventType } from './events';
import { create } from 'zustand';
import i18n from '../i18n';
import { api } from './api';
import {
  DEFAULT_UI_SETTINGS,
  applyUISettings,
  cacheUISettings,
  readCachedUISettings,
  type UISettings,
} from './appearance';
import { reportFrontendError } from './frontendErrors';
import { followLinkTarget } from './linkTarget';
import { measureInteraction, scheduleFlushCommit } from './perfMetrics';
import {
  coalesceStreamEvents,
  isReasoningDelta,
  streamFlushInterval,
} from './stream';
import { sameWorkspacePath, workspaceLabel } from './workspacePath';
import type {
  AgentSummary,
  AttachmentView,
  AutomationRun,
  AutomationTask,
  ConfigStatus,
  FileTab,
  InteractDTO,
  ModelOption,
  ReplyRequest,
  ResolvedTarget,
  SessionDefaults,
  SessionMeta,
  StreamDelta,
  StreamPart,
  TurnMessage,
  UIEvent,
  UsageDTO,
  WorkspaceMeta,
} from './types';
import type { ToolPage } from '../components/ToolsPanel';
import { routeBackendEvent, type EventDataSink } from '../state/eventRouter';
import { stateRoot } from '../state/app';
import {
  submissionEffect,
  type SubmitKind,
} from '../state/machines/conversation';
import {
  ConversationState,
  FileViewerState,
  MessageView,
  QueuedInput,
  SteerState,
  ToastItem,
  ToastKind,
  attachmentPart,
  capUndeliveredSteers,
  carryUndeliveredSteers,
  emptyConv,
  errorMessage,
  measureTranscript,
  newID,
  newTurnID,
  settleConversation,
  settleSteerDelivery,
  viewerDefaults,
} from './conv/model';
import {
  artifactTurnIndex,
  liveRunTurn,
  liveTurnEntry,
  mergeTurnDoc,
  normalizeTurnStatus,
} from './conv/artifacts';
import {
  HISTORY_PAGE_TURNS,
  INITIAL_HISTORY_TURNS,
  TAIL_SYNC_TURNS,
  foldArchivedTurns,
  historyPage,
  historyTurnsToState,
  newestArchivedSeq,
} from './conv/history';
import { applyStream } from './conv/stream';
import { recordFlushDuration } from './stream';
export {
  firstMessageTitle,
  itemText,
  pendingConversationIDs,
} from './conv/model';
export type {
  AssistantItem,
  ConversationState,
  FileViewerState,
  MessageView,
  QueuedInput,
  QueuedSteer,
  SteerState,
  TextItemFields,
  ToastItem,
  ToastKind,
  ToolView,
  TranscriptFootprint,
  TurnArtifacts,
  TurnDoc,
  TurnStatus,
} from './conv/model';
export {
  friendlyFailure,
  friendlyInterruption,
  isUserStop,
} from './conv/failure';
export { streamFlushStats } from './stream';

// gitPickNonce numbers the viewer → Git panel handoffs. A counter (not
// a timestamp) so two clicks in the same millisecond stay distinct.
let gitPickNonce = 0;

// storeBytes reports what the loaded transcripts hold, the two byte
// budgets and the conversation count. The perf probe reports it every
// window: `proc.mem.footprint` says what the renderer costs, and these
// say how much of it the store can account for.
export function storeBytes(): {
  media: number;
  text: number;
  conversations: number;
} {
  const state = useStore.getState();
  let media = 0;
  let text = 0;
  let conversations = 0;
  for (const conv of Object.values(state.conversations)) {
    conversations += 1;
    const footprint =
      conv.footprint && conv.footprint.messages === conv.messages
        ? conv.footprint
        : measureTranscript(conv.messages);
    media += footprint.media;
    text += footprint.text;
  }
  return { media, text, conversations };
}

// sameWorkspaceList reports whether a refreshed history equals the one
// already in the store. Refreshes fire on every ready event and after
// every switch, and an unchanged list must not replace the array: the
// sidebar re-flattens its history tree off it.
function sameWorkspaceList(
  prev: WorkspaceMeta[],
  next: WorkspaceMeta[],
): boolean {
  if (prev === next) return true;
  if (prev.length !== next.length) return false;
  return prev.every((w, i) => {
    const other = next[i];
    return (
      w.id === other.id &&
      w.path === other.path &&
      w.title === other.title &&
      w.last_opened === other.last_opened
    );
  });
}

interface StoreState {
  status: ConfigStatus | null;
  configured: boolean;
  fatal: string | null;
  configOpen: boolean;
  configTab: string;
  // paletteOpen drives the ⌘K command palette, which is mounted once by
  // the shell (not by whoever opened it).
  paletteOpen: boolean;
  // shortcutsOpen drives the ⌘/ shortcut sheet, the same way: mounted
  // once, opened from anywhere (key, palette, macOS Help menu).
  shortcutsOpen: boolean;
  toolsView: ToolPage | null;
  // viewers keeps one file-viewer state per conversation id.
  viewers: Record<string, FileViewerState>;
  workspace: string;
  agents: AgentSummary[];
  sessions: SessionMeta[];
  automations: AutomationTask[];
  automationRuns: Record<string, AutomationRun[]>;
  conversations: Record<string, ConversationState>;
  // pendingPromptConvs maps a pending interact/prompt id to the
  // conversation that owns it, so routing and the sidebar never scan
  // every conversation on each store update.
  pendingPromptConvs: Record<string, string>;
  // composerDraft is a one-shot draft injected into the chat composer
  // (used by the automations "create with OpenCraft" flow).
  composerDraft: string;
  statusText: string;
  lastUsage: UsageDTO | null;
  modelOptions: ModelOption[];
  sessionDefaults: SessionDefaults;
  yoloOnly: boolean;
  theme: 'dark' | 'light' | 'auto';
  /**
   * The theme with auto already resolved (the class on documentElement).
   * Kept in the store so a JS-picked colour scheme — CodeMirror's — can
   * subscribe to it; CSS reads the class itself.
   */
  resolvedTheme: 'dark' | 'light';
  // Appearance preferences (Settings > Interface). The cached copy paints
  // the first frame; the Go desktop document is the durable source and
  // reconciles this value during init.
  uiSettings: UISettings;
  workspaces: WorkspaceMeta[];
  toasts: ToastItem[];
  sessionsLoading: boolean;

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  flushStreams: () => void;
  send: (text: string, attachments?: AttachmentView[]) => Promise<void>;
  // sendInterrupt submits while a turn is running: the backend's
  // session start interrupts the active turn (barge-in) and starts
  // the replacement as soon as the old one has been finalized. The
  // composer calls it for Cmd/Ctrl+Enter; the Stop button covers the
  // plain interrupt, which carries no message.
  sendInterrupt: (
    text: string,
    attachments?: AttachmentView[],
  ) => Promise<boolean>;
  // steer submits while a turn is running: the engine delivers the text
  // at its next round boundary, not after the turn. The optimistic row
  // is drawn first; on rejection the local turn state decides — an
  // already-ended turn sends the text as a normal turn, a live one
  // reports the failure and keeps the draft. The return value says
  // whether the conversation took the text over (steered, resent, or
  // carded), which is when the caller clears its draft.
  steer: (text: string, attachments?: AttachmentView[]) => Promise<boolean>;
  // resendSteer turns an undelivered steer row into a regular turn
  // (staging it behind a running turn like a Tab draft); dismissSteer
  // drops the row and with it the only copy of its text.
  resendSteer: (id: string) => Promise<void>;
  dismissSteer: (id: string) => void;
  // queueInput stages the single draft that fires after the current
  // turn ends. Returns false when nothing was queued.
  queueInput: (text: string, attachments?: AttachmentView[]) => boolean;
  // takeQueued pops the staged draft so the caller can restore it
  // into the composer (the queue banner's X); clearQueued drops it.
  takeQueued: () => QueuedInput | undefined;
  clearQueued: () => void;
  forkTurn: (runID: string) => Promise<void>;
  clearLastFailed: () => void;
  replyInteract: (id: string, req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openConfig: (tab?: string) => void;
  closeConfig: () => void;
  openPalette: () => void;
  closePalette: () => void;
  togglePalette: () => void;
  openShortcuts: () => void;
  closeShortcuts: () => void;
  toggleShortcuts: () => void;
  openTools: (view: ToolPage) => void;
  closeTools: () => void;
  openFiles: () => void;
  closeFiles: () => void;
  // openFileTarget is the single link/file opening router: URL
  // schemes go to the system browser; local targets resolve under the
  // document base and open in the viewer.
  openFileTarget: (target: string, base?: string) => Promise<void>;
  openResolvedTarget: (res: ResolvedTarget) => void;
  closeFileTab: (key: string) => void;
  activateFileTab: (key: string) => void;
  setPanelMode: (mode: 'files' | 'git') => void;
  focusGitPath: (path: string) => void;
  consumeGitPick: () => void;
  showFileDir: (rel: string) => void;
  // newEmptyTab opens a blank placeholder tab with the file tree
  // visible, so the user can pick a file without an extra "+" row.
  newEmptyTab: () => void;
  newChat: () => Promise<void>;
  resume: (id: string) => Promise<void>;
  // loadEarlierHistory pulls the next page of archived turns below the
  // ones already loaded and prepends them. It resolves to the number of
  // message rows that were added, so the transcript can widen its render
  // window by exactly that much and keep the viewport anchored.
  loadEarlierHistory: (id: string) => Promise<number>;
  retryTranscript: (id: string) => Promise<void>;
  backFromFailure: () => void;
  deleteSession: (id: string) => Promise<void>;
  setMode: (mode: string) => Promise<void>;
  setThink: (level: string) => Promise<void>;
  setModel: (model: string) => Promise<void>;
  setTheme: (theme: 'dark' | 'light' | 'auto') => void;
  setUISettings: (settings: UISettings) => void;
  setSessionDefaults: (d: SessionDefaults) => void;
  loadWorkspaces: () => Promise<void>;
  chooseWorkspace: () => Promise<void>;
  openWorkspace: (path: string) => Promise<void>;
  openDraftChat: () => void;
  restoreWorkspaceSession: (workDir: string) => Promise<void>;
  openSessionInWorkspace: (
    sessionID: string,
    workspacePath: string,
  ) => Promise<void>;
  sendFirstMessage: (
    workspacePath: string,
    text: string,
    attachments?: AttachmentView[],
    options?: {
      mode?: string;
      think?: string;
      model?: string;
    },
  ) => Promise<boolean>;
  removeWorkspace: (id: string) => Promise<void>;
  draftComposer: (text: string) => void;
  clearComposerDraft: () => void;
  refreshAgents: () => Promise<void>;
  loadSessions: () => Promise<void>;
  loadAutomations: () => Promise<void>;
  loadAutomationRuns: (taskId: string) => Promise<void>;
  flash: (text: string) => void;
  toast: (text: string, kind?: ToastKind) => void;
  dismissToast: (id: number) => void;
}

let themeMedia: MediaQueryList | null = null;

let themeMediaHandler: (() => void) | null = null;

// applyTheme resolves dark/light/auto (auto follows the OS preference)
// and keeps a media-query listener alive while auto is selected. It
// returns the resolved value, which the store mirrors as `resolvedTheme`:
// a surface whose colours are picked in JS rather than read from a CSS
// variable (the file viewer's CodeMirror theme) has no way to notice a
// class flip otherwise.
function applyTheme(theme: 'dark' | 'light' | 'auto'): 'dark' | 'light' {
  const mq = window.matchMedia('(prefers-color-scheme: dark)');
  const resolved = theme === 'auto' ? (mq.matches ? 'dark' : 'light') : theme;
  document.documentElement.classList.toggle(
    'theme-light',
    resolved === 'light',
  );
  if (theme === 'auto') {
    if (themeMedia !== mq) {
      themeMedia?.removeEventListener('change', themeMediaHandler!);
      themeMedia = mq;
      themeMediaHandler = () => {
        useStore.setState({ resolvedTheme: applyTheme('auto') });
      };
      mq.addEventListener('change', themeMediaHandler);
    }
  } else {
    themeMedia?.removeEventListener('change', themeMediaHandler!);
    themeMedia = null;
    themeMediaHandler = null;
  }
  return resolved;
}

// activeConversationID is the session the focus state machine currently
// shows. It lives at module scope because renderer diagnostics (the perf
// probe's labels) report the same value the store's actions act on.
export function activeConversationID(): string {
  const snapshot = stateRoot.focusSnapshot;
  return snapshot.value === 'active'
    ? (snapshot.context as { sessionID: string }).sessionID
    : '';
}

export const useStore = create<StoreState>((set, get) => {
  let toastSeq = 0;
  let pendingStreamEvents: UIEvent[] = [];
  // Whether the queue holds nothing but reasoning deltas. Kept on
  // enqueue so the cadence question is answered once per delta instead
  // of by rescunning the whole queue (see stream.ts).
  let pendingStreamVisible = false;
  let streamFlushRAF: number | null = null;
  let streamFlushTimer: ReturnType<typeof setTimeout> | null = null;
  // lastStreamFlushAt paces stream commits (see stream.ts for the two
  // cadences); streamFlushDeadline is when the armed handle hands the
  // queue to the store.
  let lastStreamFlushAt = 0;
  let streamFlushDeadline = 0;
  // Session switches must land on the backend in the same order the
  // user requested them. Without this queue, an older resumeSession
  // can finish after a newer NewChat and move the backend context back
  // to the old session.
  let contextSwitchQueue: Promise<void> = Promise.resolve();
  // Workspace switches are applied in order. Older restores ignore
  // their result once a newer switch has been requested.
  let workspaceSwitchSeq = 0;
  // Workspace history refreshes race each other: a switch triggers one
  // reload from the "ready" event (before the backend has stamped
  // last_opened) and one from the caller once the binding returned.
  // Only the newest request may apply its snapshot.
  let workspacesLoadSeq = 0;
  // The automation list and one task's run list are refreshed by the
  // same three callers; only the newest answer of each may apply.
  let automationsLoadSeq = 0;
  const automationRunsLoadSeq = new Map<string, number>();
  let workspaceRestoreInFlight = false;
  let workspaceRestorePromise: Promise<void> | null = null;
  // suppressRestoreFor skips the automatic session restore after one
  // workspace switch. The new-chat flow uses it while a first message
  // is being sent to a different workspace: no previous session should
  // flash on screen and no empty draft should be minted on the way.
  let suppressRestoreFor: string | null = null;
  const waitForWorkspaceRestore = async () => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      if (!workspaceRestoreInFlight) return;
      if (workspaceRestorePromise) {
        await workspaceRestorePromise;
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  };
  // applyWorkspaceSwitch lands the window on one workspace: the focus
  // resets, the active workspace moves, and the workspace's saved
  // session is restored. The ready event is the normal caller; a switch
  // whose event never arrived enters through the same door (see
  // switchWorkspace), so the two cannot drift apart.
  const applyWorkspaceSwitch = (workDir: string) => {
    // Keep conversation actors and transcripts alive across a
    // workspace switch: a turn that is still running in the old
    // workspace continues to stream into its conversation. Only the
    // focus is reset; the session restore below decides which
    // conversation the new workspace shows.
    stateRoot.sendFocus({ type: 'WORKSPACE_RESET' });
    set({
      workspace: workDir,
      toolsView: null,
      configOpen: false,
      paletteOpen: false,
      shortcutsOpen: false,
    });
    void get().loadSessions();
    const restoreFor = suppressRestoreFor;
    suppressRestoreFor = null;
    if (restoreFor !== workDir) {
      const restore = get().restoreWorkspaceSession(workDir);
      workspaceRestoreInFlight = true;
      workspaceRestorePromise = restore;
      void restore.finally(() => {
        if (workspaceRestorePromise === restore) {
          workspaceRestorePromise = null;
          workspaceRestoreInFlight = false;
        }
      });
    }
  };
  // switchWorkspace waits out one workspace switch and answers the path
  // the window is on: the path asked for when the switch landed (the
  // backend announces it through "ready", and Workspace.Open has
  // already stamped the new workdir by the time the binding answers),
  // the path it moved to instead when another switch has taken the
  // window since, or '' when nothing settled.
  //
  // The announcement is not guaranteed to arrive. The backend skips it
  // when the workspace asked for is the one it already announced, and
  // it compares canonical paths there while the window compares the
  // raw strings — a window holding the same directory under another
  // spelling would wait out the deadline and lose the click. So the
  // wait is two-phase: a short grace for the event, then the backend's
  // own answer (the binding has applied the switch by now, so its
  // Active workspace is authoritative) landed here. Landing it is
  // idempotent with a late event: the ready handler compares the
  // workspace and does nothing when it already matches.
  const switchWorkspace = async (
    workspacePath: string,
    timeoutMs: number,
  ): Promise<string> => {
    const startedAt = Date.now();
    const deadline = startedAt + timeoutMs;
    const grace = Math.min(250, timeoutMs);
    while (
      get().workspace !== workspacePath &&
      Date.now() - startedAt < grace
    ) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    if (get().workspace === workspacePath) return workspacePath;
    const active = await api.workspace().catch(() => '');
    const held = get().workspace;
    if (sameWorkspacePath(held, workspacePath)) {
      // The switch landed while the backend was answering: the window
      // already holds it, under whatever spelling, which is what this
      // call answers.
      return held;
    }
    if (active && !sameWorkspacePath(active, workspacePath)) {
      // The backend is on a workspace this click never asked for: a
      // newer switch owns the window now, whether or not the window has
      // already followed it there. Waiting for an announcement that
      // will never come would only delay a click that has lost.
      return active;
    }
    if (active) {
      // The backend is on the workspace this click asked for: land the
      // window on it.
      if (active !== held) applyWorkspaceSwitch(active);
      return active;
    }
    // The backend would not say where it is, and the window has not moved
    // either. The announcement is the only thing left that could settle
    // this.
    while (get().workspace !== workspacePath && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    if (get().workspace === workspacePath) return workspacePath;
    // Nothing settled. Every caller turns this into an error the reader
    // sees once and the click path catches, so the log would never hear
    // about a missed switch; report it here, where both strings are
    // known, so the next one can be diagnosed from the log the way this
    // one could not.
    reportFrontendError(
      'workspace-switch',
      new Error('workspace switch did not complete'),
      `wanted ${workspaceLabel(workspacePath)}, active ${workspaceLabel(get().workspace)}`,
    );
    return '';
  };
  const runContextSwitch = <T>(op: () => Promise<T>) => {
    const next = contextSwitchQueue.then(op);
    contextSwitchQueue = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  };
  // updateConv applies one patch to a conversation and settles it (see
  // settleConversation). Everything one event writes belongs in that
  // single update: one event is one store write, which is what the
  // stream flush's flush-to-frame probe measures.
  const updateConv = (id: string, patch: Partial<ConversationState>) =>
    set((state) => {
      const conv = state.conversations[id];
      if (!conv) return state;
      return {
        conversations: {
          ...state.conversations,
          [id]: settleConversation({ ...conv, ...patch }),
        },
      };
    });

  // syncPendingIndex rebuilds the prompt-id -> conversation map for one
  // conversation after its pendingInteracts change. Stream deltas never
  // touch this map, so sidebar/interact selectors stay O(1) instead of
  // scanning every loaded conversation on each token.
  const syncPendingIndex = (conversationID: string) => {
    set((state) => {
      const conv = state.conversations[conversationID];
      const promptIDs = new Set(
        (conv?.pendingInteracts ?? []).map((p) => p.id),
      );
      const pendingPromptConvs = { ...state.pendingPromptConvs };
      let changed = false;
      for (const [promptID, ownerID] of Object.entries(pendingPromptConvs)) {
        if (ownerID !== conversationID) continue;
        if (promptIDs.delete(promptID)) continue;
        delete pendingPromptConvs[promptID];
        changed = true;
      }
      for (const promptID of promptIDs) {
        pendingPromptConvs[promptID] = conversationID;
        changed = true;
      }
      if (!changed) return state;
      return { pendingPromptConvs };
    });
  };

  const clearPendingIndex = (conversationID: string) => {
    set((state) => {
      const pendingPromptConvs = { ...state.pendingPromptConvs };
      let changed = false;
      for (const [promptID, ownerID] of Object.entries(pendingPromptConvs)) {
        if (ownerID !== conversationID) continue;
        delete pendingPromptConvs[promptID];
        changed = true;
      }
      if (!changed) return state;
      return { pendingPromptConvs };
    });
  };

  // ensureConversation returns the conversation, creating a busy shell
  // when it is unknown (a live turn resumed after a frontend reload
  // routes by conversation_id). Returns undefined only when the id is
  // empty.
  const ensureConversation = (convID: string | undefined) => {
    if (!convID) return undefined;
    let conv = get().conversations[convID];
    if (!conv) {
      set((state) => ({
        conversations: {
          ...state.conversations,
          [convID]: {
            ...emptyConv(),
          },
        },
      }));
      conv = get().conversations[convID];
    }
    return conv;
  };

  // startTurnFor is the shared low-level send: it validates the draft,
  // appends the optimistic user message, and starts one turn in the
  // given conversation. Which submission starts a turn - and what that
  // does to a live or in-flight run - is the submission table's answer
  // (SubmitKind), asked by the callers and again here.
  const startTurnFor = async (
    convID: string,
    text: string,
    attachments: AttachmentView[],
    submit: SubmitKind,
  ): Promise<boolean> => {
    const trimmed = text.trim();
    if (
      (!trimmed && attachments.length === 0) ||
      !convID ||
      !get().configured
    ) {
      return false;
    }
    const conv = get().conversations[convID];
    if (!conv) return false;
    // A start the table does not allow here would leave the machine
    // behind the turn the RPC is about to begin.
    if (!submissionStarts(convID, submit)) return false;
    const messages = [
      ...conv.messages,
      {
        id: newID('msg'),
        role: 'user' as const,
        text: trimmed,
        items: [],
        attachments,
      },
    ];
    await beginTurn(convID, trimmed, messages, attachments, submit);
    return true;
  };

  // drainQueued sends the single staged draft for a conversation. It
  // is invoked when that conversation's turn reaches its terminal
  // event (Tab queue) or, for interrupt=true, as soon as the awaited
  // run starts. The draft's own kind picks the table row - an Enter
  // waits to barge in, a Tab draft takes over the turn its run left
  // behind - and a draft the table cannot fire here stays staged.
  const drainQueued = (convID: string) => {
    const queued = get().conversations[convID]?.queued;
    if (!queued) return;
    const kind: SubmitKind = queued.interrupt ? 'interrupt' : 'drain';
    if (!submissionStarts(convID, kind)) return;
    updateConv(convID, { queued: undefined });
    void startTurnFor(convID, queued.text, queued.attachments, kind);
  };

  // markFailedSend marks the live turn entry of a send that never
  // produced a run. Used when a failed barge-in leaves the previous
  // run running: the conversation has to resume that run, so the only
  // place to surface the failed send is its own turn artifact.
  const markFailedSend = (convID: string, error: string) => {
    set((state) => {
      const conv = state.conversations[convID];
      if (!conv || conv.turnArtifacts.length === 0) return state;
      const list = conv.turnArtifacts;
      const idx = list.length - 1;
      return {
        conversations: {
          ...state.conversations,
          [convID]: {
            ...conv,
            turnArtifacts: [
              ...list.slice(0, idx),
              {
                ...list[idx],
                status: 'failed',
                error,
                finishedAt: new Date().toISOString(),
              },
              ...list.slice(idx + 1),
            ],
          },
        },
      };
    });
  };

  const beginTurn = async (
    convID: string,
    text: string,
    messages: MessageView[],
    attachments: AttachmentView[],
    submit: SubmitKind,
  ) => {
    const conv = get().conversations[convID];
    // The turn belongs to the workspace that owns the conversation,
    // not to the one on screen: a Tab/Enter draft drains on the
    // terminal event of the turn it waited for, which can land long
    // after the user switched workspaces. The actor records the
    // workspace the conversation was opened in; a conversation without
    // one is being minted right here, so the active workspace owns it.
    const workspace = stateRoot.workspaceOf(convID) || get().workspace;
    const requestedAt = new Date().toISOString();
    // Keep existing turn strips that still own messages in the new
    // transcript, then open a new live turn entry at the user message
    // just appended.
    const turnArtifacts = [
      ...conv.turnArtifacts.filter((t) => t.start < messages.length),
      {
        id: newTurnID(),
        start: messages.length - 1,
        docs: [],
        requestedAt,
      },
    ];
    updateConv(convID, {
      messages,
      turnArtifacts,
    });
    const startingActor = stateRoot.registry.ensure(convID, {
      workspaceGeneration: stateRoot.generation(),
      workspace,
    });
    startingActor?.send({ type: 'SUBMIT', kind: submit });
    try {
      const parts: StreamPart[] = [];
      if (text) parts.push({ type: 'text', text });
      for (const att of attachments) {
        parts.push(attachmentPart(att));
      }
      const wire: TurnMessage = { role: 'user', content: { parts } };
      const start = await api.startTurn(convID, wire, workspace);
      const list = get().conversations[convID].turnArtifacts;
      const liveIdx = list.length - 1;
      const turnArtifacts =
        liveIdx >= 0
          ? [
              ...list.slice(0, liveIdx),
              {
                ...list[liveIdx],
                runID: start.run_id,
                requestedAt: start.requested_at || list[liveIdx].requestedAt,
                startedAt: start.started_at || new Date().toISOString(),
              },
              ...list.slice(liveIdx + 1),
            ]
          : list;
      // The run is bound where its id becomes known: the index is what
      // routes its deltas — and the terminal event that stamps the
      // strip — back to this conversation.
      stateRoot.registry.bindRun(start.run_id, convID);
      set((state) => ({
        conversations: {
          ...state.conversations,
          [convID]: {
            ...state.conversations[convID],
            turnArtifacts,
          },
        },
      }));
      startingActor?.send({ type: 'RUN_STARTED', runID: start.run_id });
      void get().loadSessions();
      // An Enter pressed while the previous send was still "starting"
      // staged interrupt=true. The run just started, so fire it now;
      // the replacement barges in through the same drain path.
      if (get().conversations[convID]?.queued?.interrupt) {
        drainQueued(convID);
      }
    } catch (err) {
      const conv = get().conversations[convID];
      if (!conv) return;
      const error = String(err);
      const snapshot = startingActor?.getSnapshot();
      const turnValue = (snapshot?.value as { turn?: string } | undefined)
        ?.turn;
      const context = (snapshot?.context ?? {}) as {
        supersededRunID?: string;
      };
      if (turnValue === 'starting' && context.supersededRunID) {
        // The barge-in start failed. Whether the superseded run is
        // still live is the actor's own knowledge — it recorded the
        // run's terminal event if one arrived while this send was
        // starting (see the machine's `starting` state) — so both outs
        // (resume watching it, absorb its ending) are START_FAILED's.
        startingActor?.send({ type: 'START_FAILED', error });
        markFailedSend(convID, error);
        // Tab drafts stay staged for the resumed run; interrupt
        // intents aimed at the start that just failed are dropped.
        const queued = conv.queued;
        if (queued?.interrupt) {
          updateConv(convID, { queued: undefined });
        }
        return;
      }
      startingActor?.send({
        type: 'TURN_ENDED',
        runID: '',
        status: 'failed',
        error,
      });
      // A staged draft no longer has a run to wait for. Interrupt
      // intents were aimed at the failed start, so drop them; Tab
      // queues still fire after the failed send settles.
      const queued = get().conversations[convID]?.queued;
      if (queued?.interrupt) {
        updateConv(convID, { queued: undefined });
      } else if (queued) {
        drainQueued(convID);
      }
    }
  };

  const eventDataSink: EventDataSink = {
    writeConversationData: (conversationID, ev) => {
      switch (ev.type) {
        case UIEventType.stream: {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            delta: StreamDelta;
          };
          const runID = data.run_id;
          if (!runID) break;
          const actor = stateRoot.registry.get(conversationID);
          if (actor) {
            const snapshot = actor.getSnapshot();
            const value = snapshot.value as unknown as { turn: string };
            const context = snapshot.context as {
              currentRunID?: string;
              lastEndedRunID?: string;
              supersededRunID?: string;
            };
            if (context.supersededRunID && runID === context.supersededRunID) {
              // A barge-in is replacing this run; its late deltas must
              // never land after the replacement's user message.
              break;
            }
            if (value.turn === 'running') {
              // A delayed delta from an earlier run must never be
              // folded into a newer run's live transcript.
              if (context.currentRunID && runID !== context.currentRunID) {
                break;
              }
            } else if (
              (value.turn === 'starting' || value.turn === 'idle') &&
              runID === context.lastEndedRunID
            ) {
              // The machine already ignores a stream for the last ended
              // run; mirror it here so data never reaches the renderer.
              break;
            }
          }
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          // The run's rows start at its own strip, opened here when the
          // conversation holds none (a run this window only met on the
          // wire — see liveRunTurn). The delta folds into the rows at or
          // after that seam, and into no row above it: those belong to
          // another turn.
          const live = liveRunTurn(conv, runID);
          // An adopted run is bound with the strip it opens: a barge-in
          // over it has to see it as live (the machine records the
          // superseded run's ending, the registry is what routes it),
          // and the turn_end that releases the run is the one that
          // stamps that strip.
          if (live.adopted) {
            stateRoot.registry.bindRun(runID, conversationID);
          }
          updateConv(conversationID, {
            turnArtifacts: live.turnArtifacts,
            messages: applyStream(conv.messages, data.delta, live.turn.start),
          });
          break;
        }
        case UIEventType.interact: {
          const spec = ev.data as InteractDTO;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          if (!conv.pendingInteracts.some((p) => p.id === spec.id)) {
            updateConv(conversationID, {
              pendingInteracts: [...conv.pendingInteracts, spec],
            });
          }
          syncPendingIndex(conversationID);
          break;
        }
        case UIEventType.resolved: {
          const data = ev.data as { id: string };
          const conv = get().conversations[conversationID];
          if (conv?.pendingInteracts.some((p) => p.id === data.id)) {
            updateConv(conversationID, {
              pendingInteracts: conv.pendingInteracts.filter(
                (p) => p.id !== data.id,
              ),
            });
            syncPendingIndex(conversationID);
          }
          break;
        }
        case UIEventType.artifact: {
          const data = ev.data as {
            run_id?: string;
            path?: string;
            bytes?: number;
          };
          if (!data.path) break;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          const idx = artifactTurnIndex(list, data.run_id);
          if (idx < 0) break;
          const docs = mergeTurnDoc(list[idx].docs, data.path, data.bytes ?? 0);
          updateConv(conversationID, {
            turnArtifacts: [
              ...list.slice(0, idx),
              { ...list[idx], docs },
              ...list.slice(idx + 1),
            ],
          });
          break;
        }
        case UIEventType.steerPending: {
          // A round boundary drained the run's steer queue: the rows it
          // carried are delivered now, while the turn keeps running.
          const data = ev.data as {
            run_id?: string;
            steer_pending?: number;
          };
          if (!data.run_id || typeof data.steer_pending !== 'number') break;
          const conv = get().conversations[conversationID];
          if (!conv) break;
          const patch = settleSteerDelivery(
            conv,
            data.run_id,
            data.steer_pending,
          );
          if (patch) updateConv(conversationID, patch);
          break;
        }
        case UIEventType.turnEnd: {
          const data = ev.data as {
            run_id?: string;
            status: string;
            error?: string;
            interrupt_cause?: string;
            error_kind?: string;
            request_id?: string;
            response_id?: string;
            finished_at?: string;
            duration_ms?: number;
            compaction?: {
              folds: number;
              failures?: number;
              notified?: boolean;
            };
            // steer_pending counts the steered messages this turn ended
            // without delivering. A zero is a real zero (everything made
            // it); null — or the field missing entirely, which a producer
            // this build does not know about would send — means the
            // backend could not read the count, and every steered row for
            // the run is marked undelivered instead: the transcript rows
            // are the only copy of that text and archive reconciliation
            // rebuilds the turn from the archive, which never saw them.
            steer_pending?: number | null;
          };
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const finishedAt = data.finished_at || new Date().toISOString();
          const endingRun = data.run_id ?? '';
          // Which run the conversation is on right now. The actor has not
          // consumed this terminal event yet (the data layer runs first),
          // so what this names is either the ending run itself or the
          // replacement a barge-in started — and the rows handed to that
          // replacement are still waiting for a boundary of their own.
          const liveRunID = (
            stateRoot.registry.get(conversationID)?.getSnapshot().context as
              { currentRunID?: string } | undefined
          )?.currentRunID;
          // The run is released with its terminal event: from here the
          // transcript holds everything the run will ever report.
          if (endingRun) stateRoot.registry.releaseRun(endingRun);
          set((state) => {
            const conv = state.conversations[conversationID];
            if (!conv) return state;
            const turnArtifacts = conv.turnArtifacts.map((t) =>
              t.runID && t.runID === data.run_id
                ? {
                    ...t,
                    finishedAt,
                    durationMs:
                      data.duration_ms !== undefined
                        ? data.duration_ms
                        : t.durationMs,
                    status: normalizeTurnStatus(data.status) ?? t.status,
                    error: data.error ?? t.error,
                    interruptCause: data.interrupt_cause ?? t.interruptCause,
                    errorKind: data.error_kind ?? t.errorKind,
                    requestID: data.request_id ?? t.requestID,
                    responseID: data.response_id ?? t.responseID,
                    compaction: data.compaction ?? t.compaction,
                  }
                : t,
            );
            // Steer bookkeeping: the run's steered messages are FIFO, so
            // the undelivered ones are the newest entries registered for
            // it. Their text never reached the archive, so the rows stay
            // in the transcript — marked as undelivered, with their
            // resend/discard actions — instead of being dropped by the
            // archive reconciliation that follows this event.
            const tracked = (conv.steerSent ?? []).filter(
              (s) => s.runID === endingRun,
            );
            const rawPending = data.steer_pending;
            const pending =
              rawPending === null || rawPending === undefined
                ? tracked.length
                : Math.max(0, Math.floor(rawPending));
            const undelivered = pending > 0 ? tracked.slice(-pending) : [];
            const states = new Map<string, SteerState>();
            for (const s of tracked) states.set(s.messageID, 'delivered');
            for (const s of undelivered) states.set(s.messageID, 'undelivered');
            // A row handed to a run that is neither the one that just
            // ended nor the live one can never be classified anymore: its
            // terminal event was dropped, or a barge-in replaced the turn
            // before that run reported. Settling it as undelivered is the
            // honest answer — the archive keeps the text of everything a
            // boundary did take, and a rebuild retags those rows — where
            // leaving it pending would promise a boundary that never
            // comes.
            for (const s of conv.steerSent ?? []) {
              if (s.runID === endingRun || s.runID === liveRunID) continue;
              states.set(s.messageID, 'undelivered');
            }
            const messages =
              states.size > 0
                ? capUndeliveredSteers(
                    conv.messages.map((m) => {
                      const next = states.get(m.id);
                      return next ? { ...m, steer: next } : m;
                    }),
                  )
                : conv.messages;
            return {
              conversations: {
                ...state.conversations,
                [conversationID]: settleConversation({
                  ...conv,
                  messages,
                  turnArtifacts,
                  steerSent: (conv.steerSent ?? []).filter(
                    (s) => !states.has(s.messageID),
                  ),
                }),
              },
            };
          });
          void get().loadSessions();
          // A Tab-staged draft fires only when the run it was waiting
          // for completes. Drain before the actor applies TURN_ENDED:
          // the drain column of the submission table marks the
          // just-ended run as superseded, so the pending terminal event
          // stays inert and the next turn starts cleanly. Failed/cancelled/aborted endings keep the
          // draft staged (the composer shows how to send it), and a
          // terminal event from a superseded run must never drain a
          // draft that is waiting for the replacement.
          const staged = get().conversations[conversationID]?.queued;
          if (staged && !staged.interrupt && data.status === 'completed') {
            const actor = stateRoot.registry.get(conversationID);
            const snapshot = actor?.getSnapshot();
            const turn = (snapshot?.value as { turn?: string } | undefined)
              ?.turn;
            const context = (snapshot?.context ?? {}) as {
              currentRunID?: string;
            };
            const isWatchedRun =
              turn === 'running' && context.currentRunID === data.run_id;
            if (!isWatchedRun) break;
            drainQueued(conversationID);
          }
          break;
        }
        case UIEventType.automationRunStarted: {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            message?: string;
          };
          const runID = data.run_id;
          const at = new Date().toISOString();
          if (runID) {
            // A run the UI never sent is still a run this conversation
            // owns: binding it is what routes its deltas and the
            // turn_end that stamps its strip.
            stateRoot.registry.bindRun(runID, conversationID);
          }
          set((state) => {
            const existing = state.conversations[conversationID];
            const conv = existing ?? emptyConv();
            // An event for a run the transcript already holds adds the
            // mapping and nothing else.
            const known =
              runID !== undefined &&
              conv.turnArtifacts.some((t) => t.runID === runID);
            if (!runID || known) {
              if (existing) return {};
              return {
                conversations: {
                  ...state.conversations,
                  [conversationID]: conv,
                },
              };
            }
            // A run the UI did not start opens its own turn the same way
            // beginTurn opens a turn the user sent: the run's user row
            // (the task's message, which is exactly what the host
            // archives as this turn's user message) plus the strip that
            // owns it. Both halves matter — the row is what keeps the
            // run's live deltas (lastAssistant appends to the trailing
            // assistant row, so without it they land on the previous
            // turn) and its files (the strip's footer only draws for a
            // turn that owns rows) with the turn the archive will draw,
            // and the strip's run id is what the artifact and turn_end
            // paths look the turn up by.
            const messages = [
              ...conv.messages,
              {
                id: newID('msg'),
                role: 'user' as const,
                text: data.message ?? '',
                items: [],
                attachments: [],
              },
            ];
            return {
              conversations: {
                ...state.conversations,
                [conversationID]: {
                  ...conv,
                  messages,
                  turnArtifacts: [
                    ...conv.turnArtifacts,
                    {
                      id: newTurnID(),
                      start: messages.length - 1,
                      docs: [],
                      runID,
                      requestedAt: at,
                      startedAt: at,
                    },
                  ],
                },
              },
            };
          });
          break;
        }
      }
    },

    writeGlobalData: (ev) => {
      switch (ev.type) {
        case UIEventType.ready: {
          const data = ev.data as ConfigStatus;
          const workChanged = data.work_dir !== get().workspace;
          if (workChanged) {
            applyWorkspaceSwitch(data.work_dir);
          }
          void get().loadSessions();
          void get().loadAutomations();
          set((state) => ({
            status: data,
            configured: !data.needed,
            configOpen: workChanged ? false : state.configOpen,
            fatal: null,
          }));
          void api
            .modelOptions()
            .then((modelOptions) => set({ modelOptions }))
            .catch(() => {
              // model list refresh is best-effort; the UI keeps the
              // last known options until the next ready event.
            });
          void get().refreshAgents();
          void get().loadWorkspaces();
          break;
        }
        case UIEventType.fatal:
          set({ fatal: (ev.data as { error: string }).error ?? '' });
          break;
        case UIEventType.status:
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case UIEventType.usage:
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case UIEventType.managedRestored: {
          const ids = ((ev.data as { ids?: string[] }).ids ?? []).filter(
            (id) => id,
          );
          if (ids.length > 0) {
            get().toast(
              i18n.t('config.managedRestored', { plugins: ids.join(', ') }),
            );
          }
          break;
        }
      }
    },

    refreshSessionList: () => void get().loadSessions(),
    sessionUpdated: (id) => void syncTranscriptTail(id),
    refreshAutomations: () => void get().loadAutomations(),
    refreshAutomationRuns: (ev) => {
      const data = ev.data as AutomationRun;
      if (data?.task_id) void get().loadAutomationRuns(data.task_id);
    },
    conversationForRunID: (runID) =>
      stateRoot.registry.conversationForRun(runID),
    pendingInteractConversation: (promptID) =>
      get().pendingPromptConvs[promptID],
    activeWorkspace: () => get().workspace,
  };

  // turnSubmission answers the machine module's submission table: what
  // this submission may do in the phase the conversation's turn is in
  // (see SUBMISSIONS in the machine module - the guards of its SUBMIT
  // transitions read the same cells). Every path that hands the turn
  // something new asks it before spending an IPC or drawing a row.
  const turnSubmission = (conversationID: string, kind: SubmitKind) =>
    submissionEffect(
      stateRoot.registry.get(conversationID)?.getSnapshot(),
      kind,
    );

  // submissionStarts is the two cells that begin a turn: a quiet turn
  // takes one, and a live run gives it up to a barge.
  const submissionStarts = (conversationID: string, kind: SubmitKind) => {
    const effect = turnSubmission(conversationID, kind);
    return effect === 'start' || effect === 'barge';
  };

  const conversationTurnState = (conversationID: string) => {
    const actor = stateRoot.registry.get(conversationID);
    if (!actor) return { name: 'idle' as const };
    const value = actor.getSnapshot().value as { turn: string };
    const context = actor.getSnapshot().context as {
      currentRunID?: string;
      turnStage?: string;
      turnError?: string;
      failureStatus?: string;
      supersededRunID?: string;
    };
    switch (value.turn) {
      case 'starting':
        return {
          name: 'starting' as const,
          supersededRunID: context.supersededRunID,
        };
      case 'running':
        return {
          name: 'running' as const,
          runID: context.currentRunID ?? '',
          stage: context.turnStage ?? '',
        };
      case 'failed':
        return {
          name: 'failed' as const,
          error: context.turnError,
        };
      default:
        return { name: value.turn as 'idle' | 'succeeded' };
    }
  };

  const clearStreamFlushHandles = () => {
    if (streamFlushRAF !== null) cancelAnimationFrame(streamFlushRAF);
    if (streamFlushTimer !== null) clearTimeout(streamFlushTimer);
    streamFlushRAF = null;
    streamFlushTimer = null;
  };

  const flushPendingStreams = () => {
    clearStreamFlushHandles();
    if (pendingStreamEvents.length === 0) return;
    const events = pendingStreamEvents;
    pendingStreamEvents = [];
    pendingStreamVisible = false;
    const started = performance.now();
    for (const ev of coalesceStreamEvents(events)) {
      routeBackendEvent(ev, { root: stateRoot, data: eventDataSink });
    }
    lastStreamFlushAt = performance.now();
    recordFlushDuration(performance.now() - started);
    // The commit lands on the next frame; perfMetrics turns that into the
    // flush-to-frame number the probe reports.
    scheduleFlushCommit(started);
  };

  const scheduleStreamFlush = () => {
    const flush = () => flushPendingStreams();
    const now = performance.now();
    const interval = streamFlushInterval(
      pendingStreamEvents,
      !pendingStreamVisible,
    );
    const dueAt = lastStreamFlushAt + interval;
    if (streamFlushRAF !== null || streamFlushTimer !== null) {
      // A handle is already armed. Only a queue that wants an earlier
      // commit than that handle carries may pull it in — prose arriving
      // in the middle of a reasoning burst, or a tool call landing on
      // the same beat; a queue that wants a later one never pushes it
      // out, so nothing waiting is delayed past its own cadence.
      if (dueAt >= streamFlushDeadline) return;
      clearStreamFlushHandles();
    }
    if (now >= dueAt) {
      // The queue has been idle: commit on the next frame so the first
      // delta of a burst lands immediately instead of waiting out the
      // interval.
      streamFlushRAF =
        typeof requestAnimationFrame === 'function'
          ? requestAnimationFrame(flush)
          : null;
      if (streamFlushRAF === null) streamFlushTimer = setTimeout(flush, 0);
      streamFlushDeadline = now;
      return;
    }
    streamFlushTimer = setTimeout(flush, dueAt - now);
    streamFlushDeadline = dueAt;
  };

  // retainLiveConversations drops transcripts that are neither focused
  // nor backed by a live run, so merely opening many sessions over time
  // does not grow the in-memory store without bound. Resuming one of
  // those sessions hydrates from the archive again.
  const retainLiveConversations = (currentID: string) => {
    const state = get();
    const conversations: Record<string, ConversationState> = {};
    const dropped: string[] = [];
    for (const [id, conv] of Object.entries(state.conversations)) {
      if (id === currentID) {
        conversations[id] = conv;
        continue;
      }
      const actor = stateRoot.registry.get(id);
      const turnValue = actor?.getSnapshot().value as
        { turn: string } | undefined;
      const running =
        turnValue?.turn === 'starting' || turnValue?.turn === 'running';
      const mappedRun = stateRoot.registry.hasRun(id);
      const hasPendingPrompt = conv.pendingInteracts.length > 0;
      if (running || mappedRun || hasPendingPrompt) {
        conversations[id] = conv;
      } else {
        dropped.push(id);
      }
    }
    if (dropped.length === 0) return;
    // The viewers of the dropped conversations go with them: the entry
    // is a handful of tab/diff ids per conversation ever opened, and
    // resuming a session rebuilds it from the store's own defaults.
    const viewers = { ...get().viewers };
    for (const id of dropped) delete viewers[id];
    set(() => ({ conversations, viewers }));
    for (const id of dropped) stateRoot.registry.release(id);
  };

  // reconcileTurnFromArchive replaces a finished live turn with the
  // archived copy. If stream deltas were coalesced too aggressively or
  // the runtime sink detached under load, turn_end repairs the
  // transcript instead of leaving a partial answer visible forever.
  const reconcileTurnFromArchive = async (
    conversationID: string,
    runID: string,
  ) => {
    if (!conversationID || !runID) return;
    const before = get().conversations[conversationID];
    if (!before || before.messages.length === 0) return;
    const workspace = get().workspace;
    const generation = stateRoot.generation();
    try {
      const turn = await api.turnByRunID(conversationID, runID);
      const state = get();
      if (
        state.workspace !== workspace ||
        stateRoot.generation() !== generation ||
        stateRoot.registry.isDeleted(conversationID)
      ) {
        return;
      }
      if (!state.conversations[conversationID]) return;
      const actor = stateRoot.registry.get(conversationID);
      if (!actor) return;
      const turnValue = actor.getSnapshot().value as { turn: string };
      if (turnValue.turn === 'starting' || turnValue.turn === 'running') {
        return;
      }
      set((stateNow) => {
        const conv = stateNow.conversations[conversationID];
        if (!conv) return stateNow;
        if (
          conv.messages.length !== before.messages.length ||
          conv.turnArtifacts.length !== before.turnArtifacts.length
        ) {
          return stateNow;
        }
        const idx = conv.turnArtifacts.findIndex(
          (t) => t.runID && t.runID === runID,
        );
        if (idx < 0) return stateNow;
        const start = conv.turnArtifacts[idx].start;
        const rebuilt = historyTurnsToState([turn]);
        const archived = rebuilt.turnArtifacts[0];
        if (!archived) return stateNow;
        // The archive never saw a steer the turn ended without
        // delivering, so the rows carrying that text travel with the
        // rebuild: they are the only copy. The archived copy of the
        // delivered ones comes back tagged by historyToMessages.
        const carried = carryUndeliveredSteers(
          conv.messages.slice(start),
          rebuilt.messages,
        );
        return {
          conversations: {
            ...stateNow.conversations,
            [conversationID]: settleConversation({
              ...conv,
              messages: [
                ...conv.messages.slice(0, start),
                ...rebuilt.messages,
                ...carried,
              ],
              turnArtifacts: [
                ...conv.turnArtifacts.slice(0, idx),
                { ...archived, start },
                ...conv.turnArtifacts.slice(idx + 1),
              ],
            }),
          },
        };
      });
      retainLiveConversations(activeConversationID());
    } catch {
      // Archive reconciliation is best-effort: a failed turn_end must
      // not leave the UI in a worse state or block the next turn.
    }
  };

  // deferredTailSyncs holds conversations whose tail sync had to wait
  // for a running turn, each with the anchor captured at that moment.
  // The anchor is what makes the wait worth it: a note written while the
  // turn ran lands in the archive below the seq that turn gets when it
  // ends, so asking later "what came after everything I hold" would miss
  // exactly the turn that was waiting.
  const deferredTailSyncs = new Map<string, number>();

  const turnIsLive = (name: string) =>
    name === 'starting' || name === 'running';

  const rememberDeferredTailSync = (id: string, anchor: number) => {
    const held = deferredTailSyncs.get(id);
    deferredTailSyncs.set(
      id,
      held === undefined ? anchor : Math.min(held, anchor),
    );
  };

  // syncTranscriptTail appends archive turns the live stream never saw
  // to a transcript that is already on screen. A delegation note is
  // written when its subagent finishes, which can be long after the turn
  // that spawned it ended: nothing streams it, so without this the card
  // only appeared on the next full hydrate.
  //
  // Idle only. A running turn owns the tail (its deltas land on the last
  // assistant row), so a request that arrives mid-turn waits for
  // turn_end; appending under a live answer would both misplace the card
  // and split the answer around it.
  const syncTranscriptTail = async (id: string, anchor?: number) => {
    const conv = get().conversations[id];
    // Nothing to repair: the transcript is not loaded (the next hydrate
    // reads the whole archive, notes included) or holds no archived turn
    // to anchor a read on.
    if (!conv) return;
    const newest = newestArchivedSeq(conv);
    const afterSeq = anchor === undefined ? newest : Math.min(anchor, newest);
    if (afterSeq === 0) return;
    if (turnIsLive(conversationTurnState(id).name)) {
      rememberDeferredTailSync(id, afterSeq);
      return;
    }
    const workspace = get().workspace;
    const generation = stateRoot.generation();
    const messageCount = conv.messages.length;
    const artifactCount = conv.turnArtifacts.length;
    try {
      const turns = (await api.turnsSince(id, afterSeq, TAIL_SYNC_TURNS)) ?? [];
      if (
        get().workspace !== workspace ||
        stateRoot.generation() !== generation ||
        stateRoot.registry.isDeleted(id)
      ) {
        return;
      }
      const anchored = get().conversations[id];
      if (!anchored) return;
      // Anything the live path did while this read was in flight owns
      // the tail now: the fold waits for the next idle moment instead of
      // landing on top of it.
      if (
        anchored.messages.length !== messageCount ||
        anchored.turnArtifacts.length !== artifactCount ||
        turnIsLive(conversationTurnState(id).name)
      ) {
        rememberDeferredTailSync(id, afterSeq);
        return;
      }
      set((state) => {
        const current = state.conversations[id];
        if (!current || current.messages.length !== messageCount) return state;
        const folded = foldArchivedTurns(current, turns);
        if (!folded) return state;
        return {
          conversations: {
            ...state.conversations,
            [id]: settleConversation(folded),
          },
        };
      });
    } catch {
      // Best-effort, like the turn reconciliation: the next hydrate
      // reads the appended turns from the archive.
    }
  };

  // drainDeferredTailSync retries a tail sync that a running turn held
  // back. Called once that turn's terminal event has been applied and
  // its archived copy reconciled, so the transcript holds the seq of the
  // turn that just finished before the read is anchored.
  const drainDeferredTailSync = async (id: string) => {
    const anchor = deferredTailSyncs.get(id);
    if (anchor === undefined) return;
    deferredTailSyncs.delete(id);
    await syncTranscriptTail(id, anchor);
  };

  const viewerPatch = (id: string | null, patch: Partial<FileViewerState>) => {
    if (!id) return;
    set((state) => ({
      viewers: {
        ...state.viewers,
        [id]: { ...(state.viewers[id] ?? viewerDefaults()), ...patch },
      },
    }));
  };

  // openMintedSession materializes a conversation the backend already
  // minted: the current pointer moved server-side, so this only drives
  // the focus machine and local shell state without another RPC.
  const openMintedSession = (
    snapshot: {
      conversation_id: string;
      mode: string;
      think: string;
      model: string;
    },
    request: number,
  ) => {
    stateRoot.sendFocus({
      type: 'OPEN_SUCCEEDED',
      request,
      sessionID: snapshot.conversation_id,
    });
    const focus = stateRoot.focusSnapshot;
    if (
      focus.value !== 'active' ||
      focus.context.sessionID !== snapshot.conversation_id
    ) {
      return;
    }
    const id = snapshot.conversation_id;
    set((state) => ({
      toolsView: null,
      conversations: {
        ...state.conversations,
        [id]: emptyConv({
          mode: snapshot.mode,
          think: snapshot.think,
          model: snapshot.model,
        }),
      },
    }));
    stateRoot.registry.ensure(id, {
      workspaceGeneration: stateRoot.generation(),
      readyEmpty: true,
      workspace: get().workspace,
    });
    retainLiveConversations(id);
  };

  return {
    status: null,
    configured: false,
    fatal: null,
    configOpen: false,
    configTab: 'general',
    paletteOpen: false,
    shortcutsOpen: false,
    toolsView: null,
    viewers: {},
    workspace: '',
    agents: [],
    sessions: [],
    automations: [],
    automationRuns: {},
    conversations: {},
    pendingPromptConvs: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    modelOptions: [],
    sessionDefaults: { mode: 'workspace', think: 'medium' },
    yoloOnly: false,
    theme: 'dark',
    resolvedTheme: 'dark',
    uiSettings: readCachedUISettings() ?? DEFAULT_UI_SETTINGS,
    workspaces: [],
    toasts: [],
    sessionsLoading: false,

    init: async () => {
      const saved = window.localStorage.getItem('opencraft.theme');
      const theme = saved === 'light' || saved === 'auto' ? saved : 'dark';
      set({ theme, resolvedTheme: applyTheme(theme) });
      try {
        const [
          status,
          workspace,
          mode,
          currentSession,
          think,
          model,
          modelOptions,
          defaults,
          profile,
        ] = await Promise.all([
          api.configStatus(),
          api.workspace(),
          api.sessionMode(),
          api.currentSession(),
          api.getThink(),
          api.getModel(),
          api.modelOptions(),
          api.sessionDefaults(),
          api.profile(),
        ]);
        set((state) => ({
          status,
          workspace,
          configured: !status.needed,
          configOpen: false,
          toolsView: null,
          conversations:
            currentSession === ''
              ? state.conversations
              : {
                  ...state.conversations,
                  // Seed the current conversation without dropping what
                  // it already holds: init's round trips take a while,
                  // and events for this session (a live stream, an
                  // artifact) can land in the store before they resolve.
                  // A fresh table would throw them away — and would take
                  // every other open conversation with it.
                  [currentSession]: {
                    ...(state.conversations[currentSession] ?? emptyConv()),
                    mode,
                    think,
                    model,
                  },
                },
          modelOptions,
          // A binding double may resolve without the new fields; fall
          // back to the canonical defaults. A genuinely missing
          // binding rejects the batch above and surfaces as fatal
          // with a retry path (shell and backend ship together).
          sessionDefaults: defaults ?? { mode: 'workspace', think: 'medium' },
          yoloOnly: profile?.yolo_only ?? false,
          theme,
        }));
        // The persisted appearance preferences win over the localStorage
        // mirror that painted the first frame. They are cosmetic, so a
        // missing or failing binding keeps the cached copy instead of
        // failing init.
        void api
          .uiSettings()
          .then((ui) => {
            if (ui === null) return;
            get().setUISettings(ui);
          })
          .catch(() => {
            // Keep the cached appearance; the user can still change it.
          });
        if (currentSession !== '') {
          stateRoot.sendFocus({
            type: 'RESTORE_FOCUS',
            sessionID: currentSession,
          });
          const actor = stateRoot.registry.ensure(currentSession, {
            workspaceGeneration: stateRoot.generation(),
            workspace: get().workspace,
          });
          const generation = stateRoot.generation();
          actor?.send({
            type: 'HYDRATE_REQUESTED',
            request: 1,
            generation,
          });
          void api
            .sessionTurns(currentSession, INITIAL_HISTORY_TURNS + 1, 0)
            .then((turns) => {
              const page = historyPage(turns, INITIAL_HISTORY_TURNS);
              set((state) => ({
                conversations: {
                  ...state.conversations,
                  [currentSession]: settleConversation({
                    ...(state.conversations[currentSession] ?? emptyConv()),
                    messages: page.messages,
                    turnArtifacts: page.turnArtifacts,
                    historySeq: page.historySeq,
                    historyHasMore: page.historyHasMore,
                  }),
                },
              }));
              actor?.send({
                type: 'HYDRATE_OK',
                request: 1,
                generation,
                empty: turns.length === 0,
              });
            })
            .catch((err) => {
              actor?.send({
                type: 'HYDRATE_FAIL',
                request: 1,
                generation,
                error: errorMessage(err),
              });
            });
        } else if (
          workspace &&
          stateRoot.focusSnapshot.value === 'no-session'
        ) {
          // The backend keeps the current conversation id only in
          // memory, so a fresh launch has no current session. Mint one
          // immediately instead of landing on the no-session screen.
          await get().newChat();
        }
        void get().refreshAgents();
        void get().loadWorkspaces();
        void get().loadSessions();
        void get().loadAutomations();
      } catch (err) {
        // A failed init must not strand the UI on the loading screen
        // forever; surface it as a fatal error with a retry path.
        set({ fatal: String(err) });
      }
    },

    handleEvent: (ev) => {
      if (ev.type === UIEventType.stream) {
        pendingStreamEvents.push(ev);
        if (!pendingStreamVisible && !isReasoningDelta(ev)) {
          pendingStreamVisible = true;
        }
        scheduleStreamFlush();
        return;
      }
      // Non-stream events are ordering boundaries: any buffered deltas
      // must land before the terminal/global event that follows them.
      flushPendingStreams();
      const turnEndData =
        ev.type === UIEventType.turnEnd
          ? (ev.data as { run_id?: string; conversation_id?: string })
          : undefined;
      const turnEndConversationID =
        turnEndData?.conversation_id ??
        (turnEndData?.run_id
          ? stateRoot.registry.conversationForRun(turnEndData.run_id)
          : undefined);
      routeBackendEvent(ev, { root: stateRoot, data: eventDataSink });
      if (turnEndConversationID) {
        // The tail sync runs after the reconciliation, not beside it:
        // the anchor it reads back is the seq the finished turn gets from
        // its archived copy, and a note appended while that turn ran sits
        // below it in archive order.
        void reconcileTurnFromArchive(
          turnEndConversationID,
          turnEndData?.run_id ?? '',
        ).then(() => drainDeferredTailSync(turnEndConversationID));
      }
    },

    flushStreams: () => flushPendingStreams(),

    send: (text, attachments = []) =>
      measureInteraction('send', async () => {
        const trimmed = text.trim();
        const state = get();
        const convID = activeConversationID();
        const conv = convID ? state.conversations[convID] : undefined;
        if (
          (!trimmed && attachments.length === 0) ||
          !convID ||
          !conv ||
          !state.configured
        ) {
          return;
        }
        // A reply is refused while the turn is live: the table's send
        // column has 'start' only for a quiet turn.
        if (!submissionStarts(convID, 'send')) return;
        // Any fresh manual send supersedes a draft that is still staged
        // (for example after the turn it was queued behind failed).
        updateConv(convID, { queued: undefined });
        await startTurnFor(convID, text, attachments, 'send');
      }),

    sendInterrupt: (text, attachments = []) =>
      measureInteraction('send', async () => {
        const trimmed = text.trim();
        const state = get();
        const convID = activeConversationID();
        const conv = convID ? state.conversations[convID] : undefined;
        if (
          (!trimmed && attachments.length === 0) ||
          !convID ||
          !conv ||
          !state.configured
        ) {
          return false;
        }
        const effect = turnSubmission(convID, 'interrupt');
        if (effect === 'stage') {
          // The awaited run has not started yet, so there is no run id
          // to interrupt: park the input and fire it as a barge-in the
          // moment it starts (the drain takes it from there).
          updateConv(convID, {
            queued: { text: trimmed, attachments, interrupt: true },
          });
          return true;
        }
        if (effect === 'refuse') return false;
        // Enter is "answer me now": drop anything staged with Tab and
        // start immediately. While a turn is running the engine
        // interrupts it; otherwise this is a normal send.
        updateConv(convID, { queued: undefined });
        await startTurnFor(convID, text, attachments, 'interrupt');
        return true;
      }),

    steer: async (text, attachments = []) => {
      const trimmed = text.trim();
      const state = get();
      const convID = activeConversationID();
      const conv = convID ? state.conversations[convID] : undefined;
      if (
        !trimmed ||
        attachments.length > 0 ||
        !convID ||
        !conv ||
        !state.configured
      ) {
        // Steer carries text only; a draft with attachments has to wait
        // for its own turn.
        return false;
      }
      // A steer goes into the live run itself: the table refuses it in
      // every other phase, and the run id is what the RPC addresses.
      if (turnSubmission(convID, 'steer') !== 'steer') return false;
      const turn = conversationTurnState(convID);
      if (turn.name !== 'running') return false;
      const runID = turn.runID;
      const row: MessageView = {
        id: newID('msg'),
        role: 'user',
        text: trimmed,
        items: [],
        attachments: [],
        // The row is stamped before the RPC so the transcript shows it as
        // an interjection from the first paint: it never looked like a
        // new turn, and turn_end only settles which of the two final
        // states it reaches.
        steer: 'pending',
      };
      // Draw the optimistic row and register it before the RPC: turn_end
      // can settle while the submission is still in flight, and it
      // classifies rows by what is registered at that moment.
      updateConv(convID, {
        messages: [...conv.messages, row],
        steerSent: [
          ...(conv.steerSent ?? []),
          { runID, messageID: row.id, text: trimmed },
        ],
      });
      try {
        await api.steerTurn(runID, trimmed);
        return true;
      } catch {
        const convNow = get().conversations[convID];
        const drawn = (convNow?.messages ?? []).find((m) => m.id === row.id);
        const patch: Partial<ConversationState> = {
          steerSent: (convNow?.steerSent ?? []).filter(
            (s) => s.messageID !== row.id,
          ),
        };
        // Only the row still waiting for a boundary is pulled back out. A
        // row turn_end settled belongs to the conversation (the archive
        // has the text, or the row keeps it as undelivered), and one a
        // transcript rebuild replaced is archived text too: neither is
        // this submission's to take back.
        const waiting = drawn?.steer === 'pending';
        if (waiting) {
          patch.messages = (convNow?.messages ?? []).filter(
            (m) => m.id !== row.id,
          );
        }
        // Whether the text landed somewhere: turn_end settles a steer by
        // stamping the row and dropping its registration, so a
        // registration still standing means nothing took this text — a
        // row a rebuild replaced is not evidence of an archived copy.
        const settled = !(convNow?.steerSent ?? []).some(
          (s) => s.messageID === row.id,
        );
        updateConv(convID, patch);
        if (settled) {
          // The run's turn_end settled while this submission was in
          // flight; the conversation owns the text now (either the
          // archive has it or the row is marked undelivered).
          return true;
        }
        if (turnSubmission(convID, 'send') === 'start') {
          // The run ended between submit and rejection: a normal send is
          // what the user meant, so start one instead of losing the text.
          await startTurnFor(convID, trimmed, [], 'send');
          return true;
        }
        // The turn (or its replacement) is still live: keep the draft
        // and say what happened instead of silently changing the intent.
        get().toast(i18n.t('chat.steerRejected'), 'warning');
        return false;
      }
    },

    resendSteer: async (id) => {
      const convID = activeConversationID();
      const conv = convID ? get().conversations[convID] : undefined;
      if (!convID || !conv) return;
      const row = conv.messages.find(
        (m) => m.id === id && m.steer === 'undelivered',
      );
      if (!row) return;
      // The row takes whichever shape the turn can take (the table's
      // resend column): a quiet turn takes it as a turn of its own
      // ('start'), a live one stages it the way a Tab draft would
      // ('stage'). A refused row stays where it is.
      const effect = turnSubmission(convID, 'resend');
      if (effect === 'refuse') return;
      // Only drop the row once the draft is actually staged.
      if (effect === 'stage' && !get().queueInput(row.text, [])) return;
      updateConv(convID, {
        messages: conv.messages.filter((m) => m.id !== id),
      });
      if (effect === 'start') await startTurnFor(convID, row.text, [], 'send');
    },

    dismissSteer: (id) => {
      const convID = activeConversationID();
      const conv = convID ? get().conversations[convID] : undefined;
      if (!convID || !conv) return;
      updateConv(convID, {
        messages: conv.messages.filter(
          (m) => !(m.id === id && m.steer === 'undelivered'),
        ),
      });
    },

    queueInput: (text, attachments = []) => {
      const trimmed = text.trim();
      const state = get();
      const convID = activeConversationID();
      const conv = convID ? state.conversations[convID] : undefined;
      if (
        (!trimmed && attachments.length === 0) ||
        !convID ||
        !conv ||
        !state.configured
      ) {
        return false;
      }
      // The table's queue column stages a draft exactly when there is
      // a live turn to wait for.
      if (turnSubmission(convID, 'queue') !== 'stage') return false;
      // A single queue slot: a later Tab replaces the staged draft.
      updateConv(convID, {
        queued: { text: trimmed, attachments, interrupt: false },
      });
      return true;
    },

    takeQueued: () => {
      const convID = activeConversationID();
      const conv = convID ? get().conversations[convID] : undefined;
      if (!convID || !conv?.queued) return undefined;
      const staged = conv.queued;
      updateConv(convID, { queued: undefined });
      return staged;
    },

    clearQueued: () => {
      const convID = activeConversationID();
      if (convID) updateConv(convID, { queued: undefined });
    },

    forkTurn: async (runID) => {
      const convID = activeConversationID();
      if (!convID || !runID) return;
      try {
        const newID = await runContextSwitch(() => api.forkTurn(convID, runID));
        if (!newID) return;
        await get().resume(newID);
        void get().loadSessions();
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    clearLastFailed: () => {
      const convID = activeConversationID();
      if (!convID) return;
      const turn = conversationTurnState(convID);
      if (turn.name === 'failed') {
        stateRoot.registry.get(convID)?.send({ type: 'DISMISS_FAILURE' });
      }
    },

    replyInteract: async (id, req) => {
      try {
        await api.replyPrompt(id, req);
      } catch (err) {
        // Keep the card on failure: the backend prompt is still
        // pending, so removing it would make the interaction
        // unreachable.
        set({ statusText: String(err) });
        return;
      }
      const convID = get().pendingPromptConvs[id];
      const conv = convID ? get().conversations[convID] : undefined;
      if (conv && conv.pendingInteracts.some((p) => p.id === id)) {
        updateConv(convID, {
          pendingInteracts: conv.pendingInteracts.filter((p) => p.id !== id),
        });
        syncPendingIndex(convID);
      }
    },

    cancelRun: async () => {
      const convID = activeConversationID();
      const turn = convID ? conversationTurnState(convID) : undefined;
      let runID = '';
      if (turn?.name === 'running') {
        runID = turn.runID;
      } else if (turn?.name === 'starting' && turn.supersededRunID) {
        // A barge-in is waiting for the superseded run to finalize.
        // Force-cancelling it lets the pending replacement start.
        runID = turn.supersededRunID;
      }
      if (!runID) return;
      try {
        await api.cancelTurn(runID);
      } catch (err) {
        // The superseded run may already have settled while the
        // replacement was starting; that is the happy path, so a
        // "turn not found" failure must not surface as an error.
        if (
          !/not found|not active|already (ended|finished)/i.test(String(err))
        ) {
          // Surface real cancel failures instead of leaving the UI
          // running silently; a real cancel settles via turn_end.
          set({ statusText: String(err) });
        }
      }
    },

    openConfig: (tab) => {
      // Opening the settings page is a render the probe attributes: the
      // measurement runs to the frame that shows it.
      void measureInteraction('settings-open', () =>
        set({ configOpen: true, configTab: tab ?? 'general' }),
      );
    },
    closeConfig: () => set({ configOpen: false }),

    openPalette: () => set({ paletteOpen: true }),
    closePalette: () => set({ paletteOpen: false }),
    togglePalette: () => set((state) => ({ paletteOpen: !state.paletteOpen })),

    openShortcuts: () => set({ shortcutsOpen: true }),
    closeShortcuts: () => set({ shortcutsOpen: false }),
    toggleShortcuts: () =>
      set((state) => ({ shortcutsOpen: !state.shortcutsOpen })),

    openTools: (view) => set({ toolsView: view, configOpen: false }),
    closeTools: () => set({ toolsView: null }),

    openFiles: () => viewerPatch(activeConversationID(), { filesOpen: true }),
    closeFiles: () => viewerPatch(activeConversationID(), { filesOpen: false }),

    // openFileTarget is the chat's link/file opening router: URL
    // schemes reach the system browser through the validated binding,
    // local targets resolve under the document base into the viewer
    // panel of the active conversation. Workspace directories open the
    // file tree; the other roots have no tree and go to the system file
    // manager instead.
    openFileTarget: (target, base = '') =>
      followLinkTarget(target, base, {
        openFile: (res) => get().openResolvedTarget(res),
        openDir: (res) => {
          if (res.root !== 'workspace') return api.revealArtifact(res.path);
          viewerPatch(activeConversationID(), {
            filesOpen: true,
            fileTreeDir: res.rel || '.',
          });
        },
        onError: (message) => get().flash(message),
      }),

    openResolvedTarget: (res) =>
      set((state) => {
        const id = activeConversationID();
        if (!id) return {};
        const viewer = state.viewers[id] ?? viewerDefaults();
        // Placeholder tabs are transient: picking a real file from the
        // tree replaces the active blank tab instead of stacking next
        // to it. Other placeholder tabs (if any) stay untouched.
        const tabs = viewer.fileTabs.filter(
          (t) =>
            t.key !== res.path &&
            !(t.path === '' && t.key === viewer.fileActive),
        );
        tabs.push({
          key: res.path,
          path: res.path,
          rel: res.rel,
          root: res.root,
          name:
            res.name ||
            (res.rel ? (res.rel.split('/').pop() ?? res.rel) : res.path),
          media_type: res.media_type ?? '',
        });
        return {
          viewers: {
            ...state.viewers,
            [id]: {
              ...viewer,
              filesOpen: true,
              panelMode: 'files',
              fileTabs: tabs,
              fileActive: res.path,
            },
          },
        };
      }),

    closeFileTab: (key) =>
      set((state) => {
        const id = activeConversationID();
        if (!id) return {};
        const viewer = state.viewers[id] ?? viewerDefaults();
        const tabs = viewer.fileTabs.filter((t) => t.key !== key);
        const active =
          viewer.fileActive === key
            ? (tabs[tabs.length - 1]?.key ?? null)
            : viewer.fileActive;
        return {
          viewers: {
            ...state.viewers,
            [id]: { ...viewer, fileTabs: tabs, fileActive: active },
          },
        };
      }),

    activateFileTab: (key) =>
      viewerPatch(activeConversationID(), { fileActive: key }),
    setPanelMode: (mode) =>
      viewerPatch(activeConversationID(), { panelMode: mode }),
    // focusGitPath is the viewer's marks chip: switch the rail to the
    // Git segment and ask the panel to open this file's diff. The
    // nonce makes a second click on the same file a fresh handoff.
    focusGitPath: (path) =>
      viewerPatch(activeConversationID(), {
        panelMode: 'git',
        gitPick: { path, nonce: (gitPickNonce += 1) },
      }),
    // consumeGitPick retires a handoff the panel has already answered:
    // the panel is unmounted whenever the rail shows the Files segment,
    // so without this a later switch back to Git would replay the pick.
    consumeGitPick: () =>
      viewerPatch(activeConversationID(), { gitPick: undefined }),
    showFileDir: (rel) =>
      viewerPatch(activeConversationID(), {
        fileTreeDir: rel || '.',
        filesOpen: true,
      }),

    newEmptyTab: () => {
      const id = activeConversationID();
      if (!id) return;
      set((state) => {
        const viewer = state.viewers[id] ?? viewerDefaults();
        const key = `untitled-${Date.now()}-${Math.random()
          .toString(36)
          .slice(2, 7)}`;
        const tab: FileTab = {
          key,
          path: '',
          rel: '',
          root: 'workspace',
          name: i18n.t('files.newFile'),
          media_type: '',
        };
        return {
          viewers: {
            ...state.viewers,
            [id]: {
              ...viewer,
              filesOpen: true,
              fileTabs: [...viewer.fileTabs, tab],
              fileActive: key,
            },
          },
        };
      });
    },

    newChat: async () => {
      stateRoot.sendFocus({ type: 'OPEN_NEW' });
      const request = stateRoot.focusSnapshot.context.request;
      try {
        const snapshot = await runContextSwitch(() => api.newChat());
        openMintedSession(snapshot, request);
      } catch (err) {
        stateRoot.sendFocus({
          type: 'OPEN_FAILED',
          request,
          error: errorMessage(err),
        });
      }
      void get().loadSessions();
    },

    openDraftChat: () => {
      // A new chat starts as an unsent draft: no session is minted
      // until the first message picks a workspace and sends.
      stateRoot.sendFocus({ type: 'OPEN_DRAFT' });
      set({ toolsView: null, configOpen: false });
    },

    backFromFailure: () => {
      stateRoot.sendFocus({ type: 'BACK' });
    },

    // loadEarlierHistory pages backwards through the archive. The merge
    // settles the conversation — the byte budgets have to run here, this
    // is the path that pages screenshot-heavy history into the store —
    // but skips the message cap, so an explicit "scroll up" is never
    // undone by a cap that would drop the page just fetched.
    loadEarlierHistory: async (id) => {
      const conv = get().conversations[id];
      if (!conv || conv.historyLoading || !conv.historyHasMore) return 0;
      const beforeSeq = conv.historySeq ?? 0;
      updateConv(id, { historyLoading: true });
      try {
        const turns = await api.sessionTurns(
          id,
          HISTORY_PAGE_TURNS + 1,
          beforeSeq,
        );
        const page = historyPage(turns, HISTORY_PAGE_TURNS);
        const added = page.messages.length;
        set((state) => {
          const current = state.conversations[id];
          if (!current) return state;
          const shiftedTurns = current.turnArtifacts.map((t) => ({
            ...t,
            start: t.start + added,
          }));
          return {
            conversations: {
              ...state.conversations,
              [id]: settleConversation(
                {
                  ...current,
                  messages: [...page.messages, ...current.messages],
                  turnArtifacts: [...page.turnArtifacts, ...shiftedTurns],
                  // An empty page carries seq 0: keeping it would say
                  // "nothing below here is loaded" about a transcript
                  // that is, and the next incremental read would start
                  // from the beginning of the archive.
                  historySeq: added > 0 ? page.historySeq : current.historySeq,
                  historyHasMore: page.historyHasMore,
                  historyLoading: false,
                },
                { cap: false },
              ),
            },
          };
        });
        return added;
      } catch {
        updateConv(id, { historyLoading: false });
        return 0;
      }
    },

    retryTranscript: async (id) => {
      const actor = stateRoot.registry.ensure(id, {
        workspaceGeneration: stateRoot.generation(),
        workspace: get().workspace,
      });
      const context = actor?.getSnapshot().context as {
        lastHydrateRequest?: number;
      };
      const before = get().conversations[id];
      const request = (context?.lastHydrateRequest ?? 0) + 1;
      const generation = stateRoot.generation();
      actor?.send({ type: 'HYDRATE_REQUESTED', request, generation });
      try {
        const turns = await api.sessionTurns(id, INITIAL_HISTORY_TURNS + 1, 0);
        const page = historyPage(turns, INITIAL_HISTORY_TURNS);
        set((state) => ({
          conversations: {
            ...state.conversations,
            [id]: settleConversation({
              ...emptyConv(),
              mode: state.conversations[id]?.mode ?? 'workspace',
              think: state.conversations[id]?.think ?? 'medium',
              model: state.conversations[id]?.model ?? '',
              // Undelivered steer rows never entered the archive, so a
              // rebuild from archived turns has to carry them over: they
              // are the only copy of their text.
              messages: [
                ...page.messages,
                ...carryUndeliveredSteers(before?.messages, page.messages),
              ],
              turnArtifacts: page.turnArtifacts,
              historySeq: page.historySeq,
              historyHasMore: page.historyHasMore,
            }),
          },
        }));
        actor?.send({
          type: 'HYDRATE_OK',
          request,
          generation,
          empty: turns.length === 0,
        });
      } catch (err) {
        actor?.send({
          type: 'HYDRATE_FAIL',
          request,
          generation,
          error: errorMessage(err),
        });
      }
    },

    resume: (id) =>
      measureInteraction('resume', async () => {
        if (activeConversationID() === id) {
          // Returning to the already-active conversation means closing
          // whatever overlay/tool page currently covers the chat.
          set({ toolsView: null, configOpen: false });
          return;
        }
        if (stateRoot.registry.isDeleted(id)) {
          // This id was deleted in this process and the backend never
          // reuses a session id, so there is nothing to resume. Saying
          // so beats switching focus to a conversation whose actor can
          // never be created (the registry refuses tombstoned ids): the
          // chat would sit on "loading history" with no event able to
          // finish it.
          get().toast(i18n.t('chat.sessionDeleted'), 'warning');
          return;
        }
        stateRoot.sendFocus({ type: 'OPEN_SESSION', id });
        const request = stateRoot.focusSnapshot.context.request;
        try {
          const snapshot = await runContextSwitch(() => api.resumeSession(id));
          stateRoot.sendFocus({
            type: 'OPEN_SUCCEEDED',
            request,
            sessionID: snapshot.conversation_id,
          });
          const focus = stateRoot.focusSnapshot;
          if (
            focus.value !== 'active' ||
            focus.context.sessionID !== snapshot.conversation_id
          ) {
            return;
          }
          const resolvedID = snapshot.conversation_id;
          const actor = stateRoot.registry.ensure(resolvedID, {
            workspaceGeneration: stateRoot.generation(),
            workspace: get().workspace,
          });
          const hydrateRequest = 1;
          const generation = stateRoot.generation();
          actor?.send({
            type: 'HYDRATE_REQUESTED',
            request: hydrateRequest,
            generation,
          });
          const existing = get().conversations[resolvedID];
          const actorValue = actor?.getSnapshot().value as
            { transcript: string; turn: string } | undefined;
          if (existing && actorValue?.transcript === 'ready') {
            set({
              toolsView: null,
              conversations: {
                ...get().conversations,
                [resolvedID]: {
                  ...get().conversations[resolvedID],
                  mode: snapshot.mode,
                  think: snapshot.think,
                  model: snapshot.model,
                },
              },
            });
            actor?.send({
              type: 'HYDRATE_OK',
              request: hydrateRequest,
              generation,
              empty: existing.messages.length === 0,
            });
            retainLiveConversations(resolvedID);
            return;
          }
          let turns: Awaited<ReturnType<typeof api.sessionTurns>>;
          try {
            turns = await api.sessionTurns(
              resolvedID,
              INITIAL_HISTORY_TURNS + 1,
              0,
            );
          } catch (err) {
            actor?.send({
              type: 'HYDRATE_FAIL',
              request: hydrateRequest,
              generation,
              error: errorMessage(err),
            });
            if (!existing) {
              set((state) => ({
                conversations: {
                  ...state.conversations,
                  [resolvedID]: emptyConv(),
                },
              }));
            }
            return;
          }
          const page = historyPage(turns, INITIAL_HISTORY_TURNS);
          const archivedMessages = page.messages;
          // A live shell may already hold the current run's streamed
          // messages. Keep them after the archived history; completed
          // shells are replaced by the archive instead of duplicated.
          const keepLive =
            Boolean(existing) &&
            (actorValue?.turn === 'running' || actorValue?.turn === 'starting');
          const mergedMessages = keepLive
            ? [...archivedMessages, ...existing.messages]
            : [
                // Undelivered steer rows are not in the archive, so they
                // travel across the rebuild explicitly (see
                // carryUndeliveredSteers).
                ...archivedMessages,
                ...carryUndeliveredSteers(existing?.messages, archivedMessages),
              ];
          // The live run owns every row past the seam the archive ends
          // at, so it gets the strip that owns them. Without one those
          // rows read as the last archived turn's: that turn's fold
          // header names a run that already finished and its steps
          // collapse under a complete answer (see turnIsLive in
          // ChatView). The strip carries the run the actor is tracking,
          // which is what the artifact and turn_end events look their
          // turn up by, and a strip the shell already had keeps its own
          // rows (see liveTurnEntry).
          const liveRunID = (
            actor?.getSnapshot().context as
              { currentRunID?: string } | undefined
          )?.currentRunID;
          const turnArtifacts = keepLive
            ? [
                ...page.turnArtifacts,
                liveTurnEntry(
                  archivedMessages.length,
                  existing.turnArtifacts[existing.turnArtifacts.length - 1],
                  liveRunID,
                ),
              ]
            : page.turnArtifacts;
          set((state) => ({
            toolsView: null,
            conversations: {
              ...state.conversations,
              [resolvedID]: settleConversation({
                ...emptyConv(),
                mode: snapshot.mode,
                think: snapshot.think,
                model: snapshot.model,
                messages: mergedMessages,
                turnArtifacts,
                historySeq: page.historySeq,
                historyHasMore: page.historyHasMore,
                pendingInteracts: existing?.pendingInteracts ?? [],
              }),
            },
          }));
          if (existing?.pendingInteracts.length) {
            syncPendingIndex(resolvedID);
          }
          actor?.send({
            type: 'HYDRATE_OK',
            request: hydrateRequest,
            generation,
            empty: turns.length === 0 && !keepLive,
          });
          retainLiveConversations(resolvedID);
        } catch (err) {
          stateRoot.sendFocus({
            type: 'OPEN_FAILED',
            request,
            error: errorMessage(err),
          });
        }
      }),

    deleteSession: async (id) => {
      try {
        // Settle any queued deltas before deleting so a late flush
        // cannot resurrect the conversation after the tombstone.
        flushPendingStreams();
        // The backend stops any live run for the conversation and,
        // when it was current, mints its replacement in the same call.
        const next = await runContextSwitch(() => api.deleteSession(id));
        stateRoot.registry.get(id)?.send({
          type: 'SESSION_DELETED',
          deletedAt: new Date().toISOString(),
        });
        // The tombstone takes the conversation's run mappings with it:
        // a deleted chat never ends its turn, so the entries would keep
        // naming a conversation that is gone (and a late turn_end would
        // then reconcile it back onto the screen).
        stateRoot.registry.markDeleted(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          const viewers = { ...state.viewers };
          delete viewers[id];
          return { conversations, viewers };
        });
        clearPendingIndex(id);
        // A tail sync waiting on this conversation holds its id until
        // that conversation's next turn_end; there will not be one.
        deferredTailSyncs.delete(id);
        // The backend only mints when the deleted chat was still the
        // workspace's current conversation once the delete settled.
        // Open that replacement only when this chat is still focused;
        // a selection made while the delete waited wins and should not
        // be stomped by an OPEN_NEW.
        if (next.conversation_id && activeConversationID() === id) {
          stateRoot.sendFocus({ type: 'OPEN_NEW' });
          openMintedSession(next, stateRoot.focusSnapshot.context.request);
        }
        await get().loadSessions();
      } catch (err) {
        set({ statusText: errorMessage(err) });
      }
    },

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        const convID = activeConversationID();
        if (convID) updateConv(convID, { mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setThink: async (level) => {
      try {
        await api.setThink(level);
        const convID = activeConversationID();
        if (convID) updateConv(convID, { think: level });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setModel: async (model) => {
      try {
        await api.setModel(model);
        const convID = activeConversationID();
        if (!convID) return;
        const previous = get().conversations[convID]?.model ?? '';
        updateConv(convID, { model });
        // Provider prompt caches are scoped to the model, so switching
        // mid-conversation re-reads the whole transcript at undiscounted
        // input price once. Worth one line: the user otherwise reads the
        // bill (or the sudden latency) as a bug. A conversation that has
        // not exchanged anything yet has no cache to lose, so it stays
        // quiet.
        const hasContent =
          (get().conversations[convID]?.messages.length ?? 0) > 0;
        if (hasContent && previous !== '' && previous !== model) {
          get().toast(i18n.t('chat.modelSwitchCost', { model }), 'warning');
        }
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setTheme: (theme) => {
      const resolved = applyTheme(theme);
      window.localStorage.setItem('opencraft.theme', theme);
      set({ theme, resolvedTheme: resolved });
    },

    // setUISettings applies the appearance immediately and mirrors it for
    // the next first paint; persisting to the desktop document is the
    // caller's job, so a failed save can roll this back.
    setUISettings: (settings) => {
      applyUISettings(settings);
      cacheUISettings(settings);
      set({ uiSettings: settings });
    },

    setSessionDefaults: (d) => set({ sessionDefaults: d }),

    refreshAgents: async () => {
      try {
        set({ agents: (await api.listAgents()) ?? [] });
      } catch {
        // best-effort
      }
    },

    loadSessions: async () => {
      set({ sessionsLoading: true });
      try {
        const sessions = (await api.listSessions()) ?? [];
        // An id deleted in this process never comes back on screen. The
        // listing can race the delete — a response computed before the
        // backend settled, a store restored out of band — and the
        // sidebar is the one place a dead entry would look openable
        // while every path into it is refused by the same tombstone.
        set({
          sessions: sessions.filter(
            (session) => !stateRoot.registry.isDeleted(session.id),
          ),
        });
      } catch {
        // best-effort
      } finally {
        set({ sessionsLoading: false });
      }
    },

    loadAutomations: async () => {
      // Starting a task, its settlement and an explicit refresh can
      // overlap; the answer that arrives last is the one the list shows,
      // like loadWorkspaces (a stale run's list would reinstate the
      // Cancel button of a task that just started).
      const seq = (automationsLoadSeq += 1);
      try {
        const next = (await api.automations()) ?? [];
        if (seq !== automationsLoadSeq) return;
        set({ automations: next });
      } catch {
        // best-effort
      }
    },

    loadAutomationRuns: async (taskId: string) => {
      const seq = (automationRunsLoadSeq.get(taskId) ?? 0) + 1;
      automationRunsLoadSeq.set(taskId, seq);
      try {
        const list = (await api.automationRuns(taskId)) ?? [];
        if (automationRunsLoadSeq.get(taskId) !== seq) return;
        set((state) => ({
          automationRuns: {
            ...state.automationRuns,
            [taskId]: list,
          },
        }));
      } catch {
        // best-effort
      }
    },

    loadWorkspaces: async () => {
      const seq = (workspacesLoadSeq += 1);
      try {
        const next = (await api.workspaces()) ?? [];
        if (seq !== workspacesLoadSeq) return;
        if (sameWorkspaceList(get().workspaces, next)) return;
        set({ workspaces: next });
      } catch {
        // best-effort
      }
    },

    chooseWorkspace: async () => {
      try {
        const path = (await api.chooseWorkspace()) ?? '';
        if (path) await get().openWorkspace(path);
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    restoreWorkspaceSession: async (workDir) => {
      const seq = ++workspaceSwitchSeq;
      stateRoot.sendFocus({ type: 'WORKSPACE_RESET' });
      if (!workDir || get().workspace !== workDir) return;
      let currentSession = '';
      try {
        currentSession = await api.currentSession();
      } catch {
        // Fall through and mint a fresh session below.
      }
      if (seq !== workspaceSwitchSeq || get().workspace !== workDir) {
        return;
      }
      if (!currentSession) {
        await get().newChat();
        return;
      }
      await get().resume(currentSession);
      const focus = stateRoot.focusSnapshot;
      if (
        seq === workspaceSwitchSeq &&
        get().workspace === workDir &&
        focus.value !== 'active'
      ) {
        // The saved session is gone or cannot be resumed; land on a
        // fresh conversation instead of leaving the workspace blank.
        await get().newChat();
      }
    },

    openWorkspace: async (path) => {
      try {
        await api.openWorkspace(path);
        // The runtime rebuild emits "ready"; the ready handler
        // restores the target workspace's session and refreshes
        // sessions. Its workspace refresh is not enough to rely on:
        // it runs before the backend stamped last_opened (see
        // Workspace.Open), so read the history again here.
        void get().loadWorkspaces();
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    openSessionInWorkspace: async (sessionID, workspacePath) => {
      if (workspacePath && workspacePath !== get().workspace) {
        await api.openWorkspace(workspacePath);
        // The backend emits "ready" asynchronously relative to the
        // binding response; wait until the store has applied it so a
        // pending newChat is queued before resume tries to open the
        // target session in the new workspace.
        const landed = await switchWorkspace(workspacePath, 5000);
        if (!landed) {
          throw new Error('workspace switch did not complete');
        }
        if (!sameWorkspacePath(landed, workspacePath)) {
          // Another workspace's switch took the window while this one
          // was in flight: the click that is on screen now wins.
          return;
        }
        void get().loadWorkspaces();
        await waitForWorkspaceRestore();
        if (get().workspace !== landed) {
          // The window left again while this switch (and its restore)
          // was in flight: another workspace was picked in the
          // meantime. Resuming now would run through bindings that read
          // whatever workspace is active, so the click still on screen
          // wins and this one is dropped.
          return;
        }
      }
      await get().resume(sessionID);
    },

    sendFirstMessage: async (
      workspacePath,
      text,
      attachments = [],
      options,
    ) => {
      const trimmed = text.trim();
      if ((!trimmed && attachments.length === 0) || !get().configured) {
        return false;
      }
      const current = get().workspace;
      const target =
        workspacePath && workspacePath !== current ? workspacePath : current;
      if (!target) return false;
      if (target !== current) {
        suppressRestoreFor = target;
        try {
          await api.openWorkspace(target);
          const landed = await switchWorkspace(target, 8000);
          // A draft is a send to a named workspace, so a switch that did
          // not land — nothing settled, or a newer switch took the window
          // first — is reported rather than dropped the way a session
          // click is: the text goes back to the composer and the status
          // line says why it did not go.
          if (!landed || !sameWorkspacePath(landed, target)) {
            throw new Error('workspace switch did not complete');
          }
          void get().loadWorkspaces();
        } catch (err) {
          suppressRestoreFor = null;
          set({ statusText: String(err) });
          return false;
        }
        suppressRestoreFor = null;
      }
      // The draft becomes a real conversation now: mint it in the
      // selected workspace and send the staged first message.
      await get().newChat();
      if (stateRoot.focusSnapshot.value !== 'active') {
        return false;
      }
      // Draft pre-send choices land on the fresh session before its
      // first run starts. Without explicit options the minted
      // conversation already carries the configured session defaults.
      if (options) {
        if (options.mode) {
          await get().setMode(options.mode);
        }
        if (options.think) {
          await get().setThink(options.think);
        }
        await get().setModel(options.model ?? '');
      }
      await get().send(trimmed, attachments);
      return true;
    },

    removeWorkspace: async (id) => {
      try {
        await api.removeWorkspace(id);
        await get().loadWorkspaces();
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    draftComposer: (text) => {
      set({ composerDraft: text });
    },

    clearComposerDraft: () => {
      set({ composerDraft: '' });
    },

    flash: (text) => get().toast(text),
    toast: (text, kind = 'info') => {
      const id = ++toastSeq;
      set((state) => ({ toasts: [...state.toasts, { id, text, kind }] }));
      setTimeout(() => {
        set((state) => ({
          toasts: state.toasts.filter((t) => t.id !== id),
        }));
      }, 3500);
    },
    dismissToast: (id) =>
      set((state) => ({
        toasts: state.toasts.filter((t) => t.id !== id),
      })),
  };
});
