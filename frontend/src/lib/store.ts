import { UIEventType } from './events';
import { create } from 'zustand';
import { api } from './api';
import {
  DEFAULT_UI_SETTINGS,
  readCachedUISettings,
  type UISettings,
} from './appearance';
import type {
  AgentSummary,
  AttachmentView,
  AutomationRun,
  AutomationTask,
  ConfigStatus,
  ModelOption,
  ReplyRequest,
  ResolvedTarget,
  SessionDefaults,
  SessionMeta,
  UIEvent,
  UsageDTO,
  WorkspaceMeta,
} from './types';
import type { ToolPage } from '../components/ToolsPanel';
import { routeBackendEvent } from '../state/eventRouter';
import { stateRoot } from '../state/app';
import {
  ConversationState,
  FileViewerState,
  QueuedInput,
  ToastItem,
  ToastKind,
  emptyConv,
  errorMessage,
  measureTranscript,
  settleConversation,
} from './conv/model';
import { INITIAL_HISTORY_TURNS, historyPage } from './conv/history';
import { activeConversationID, createConvCore } from './conv/core';
import { createWorkspaceGlue } from './conv/workspace';
import { createConvPaging } from './conv/paging';
import { createConvTurn } from './conv/turn';
import { createEventSink } from './conv/writer';
import { turnNeedsAttention } from './conv/failure';
import { createShellViewers } from './shell/viewers';
import { createShellSettings } from './shell/settings';
import { createShellSessions } from './shell/sessions';
import { createShellHistory } from './shell/history';
import { createShellWorkspaces } from './shell/workspaces';
import { createShellAutomations } from './shell/automations';
import { createShellToasts } from './shell/toasts';
export { activeConversationID };
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

export interface StoreState {
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
  // turnAttention maps a conversation to the run of its last turn that
  // needs the reader: failed, aborted, or cut off by a deadline. It is
  // the sidebar's own memory, not the actor's — an actor only exists
  // once this window has opened the conversation, and a turn that failed
  // in a background workspace has none. Storing the run id keeps the
  // reason next to the marker for whoever says which turn it was.
  turnAttention: Record<string, string>;
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
  openFromNotification: (
    sessionID: string,
    workspacePath?: string,
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

export const useStore = create<StoreState>((set, get, store) => {
  // The bookkeeping lives in conv/core.ts; destructuring keeps the
  // bodies below reading the names they always did.
  const core = createConvCore(store);
  const ws = createWorkspaceGlue(store);
  const paging = createConvPaging(store, core);
  const { actions: turn, helpers: turnHelpers } = createConvTurn(store, core);
  const viewers = createShellViewers(store, core);
  const settings = createShellSettings(store, core, applyTheme);
  const sessions = createShellSessions(store, core, paging, ws);
  const history = createShellHistory(store, core);
  const workspaces = createShellWorkspaces(store, ws);
  const automations = createShellAutomations(store);
  const toasts = createShellToasts(store);
  const { drainDeferredTailSync, reconcileTurnFromArchive } = paging;
  const { flushPendingStreams } = core;

  const eventDataSink = createEventSink(store, core, turnHelpers, paging, ws);
  core.setEventSink(eventDataSink);
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
    turnAttention: {},
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
        core.enqueueStreamEvent(ev);
        return;
      }
      // Non-stream events are ordering boundaries: any buffered deltas
      // must land before the terminal/global event that follows them.
      flushPendingStreams();
      const turnEndData =
        ev.type === UIEventType.turnEnd
          ? (ev.data as {
              run_id?: string;
              conversation_id?: string;
              status?: string;
              error_kind?: string;
            })
          : undefined;
      const turnEndConversationID =
        turnEndData?.conversation_id ??
        (turnEndData?.run_id
          ? stateRoot.registry.conversationForRun(turnEndData.run_id)
          : undefined);
      if (turnEndData && turnEndConversationID) {
        // The sidebar's ❌ is about the conversation, not about this
        // window's actor for it: the turn may have run in another
        // workspace, or ended before this conversation was ever opened.
        // Recording it here, before routing, keeps the row and the
        // transcript from disagreeing about the same event.
        const needsAttention = turnNeedsAttention(
          turnEndData.status,
          turnEndData.error_kind,
        );
        set((state) => {
          const next = { ...state.turnAttention };
          if (needsAttention) {
            next[turnEndConversationID] = turnEndData.run_id ?? '';
          } else {
            delete next[turnEndConversationID];
          }
          return { turnAttention: next };
        });
      }
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

    ...turn,
    ...viewers,
    ...settings,
    ...sessions,
    ...history,
    ...workspaces,
    ...automations,
    ...toasts,

    draftComposer: (text) => {
      set({ composerDraft: text });
    },

    clearComposerDraft: () => {
      set({ composerDraft: '' });
    },
  };
});
