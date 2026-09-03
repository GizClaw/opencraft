import { create } from 'zustand';
import i18n from '../i18n';
import { api } from './api';
import { sanitizeToolResult } from './ansi';
import type {
  AgentSummary,
  AutomationRun,
  AutomationTask,
  AttachmentView,
  ConfigStatus,
  InteractDTO,
  KanbanCard,
  HistoryPart,
  HistoryMessage,
  ModelOption,
  ReplyRequest,
  SessionMeta,
  SessionTurn,
  StreamDelta,
  StreamPart,
  TurnMessage,
  UIEvent,
  UsageDTO,
  WorkspaceMeta,
} from './types';
import type { ToolPage } from '../components/ToolsPanel';
import {
  activeSessionID,
  displaySessionID,
  navigationReducer,
  type SessionNavigation,
  type SessionNavigationAction,
} from './sessionMachine';

export { activeSessionID, displaySessionID, type SessionNavigation };

export interface ToolView {
  id: string;
  name: string;
  args: string;
  status: 'running' | 'done' | 'error';
  result?: string;
}

// AssistantItem preserves the stream arrival order of one assistant
// block (reasoning trace, tool call, or text), so renderers can show
// output in the exact order the model produced it. The chat transcript
// drops reasoning traces; the subagent sidebar keeps them visible.
export type AssistantItem =
  | { kind: 'reasoning'; id: string; text: string }
  | { kind: 'tool_call'; id: string; tool: ToolView }
  | { kind: 'text'; id: string; text: string };

export interface MessageView {
  id: string;
  role: 'user' | 'assistant';
  // text carries the user message body; assistant messages render
  // their ordered items instead.
  text: string;
  items: AssistantItem[];
  // attachments renders user message media: images above the text,
  // other files in a collapsed list below it.
  attachments: AttachmentView[];
}

// ConversationContent tracks whether a session's transcript is
// available in the frontend. Rendering must never infer this from
// messages.length alone.
export type ConversationContent =
  | { name: 'empty' }
  | { name: 'loading' }
  | { name: 'ready' }
  | { name: 'live-shell' }
  | { name: 'failed'; error: string };

// TurnLifecycle is the explicit run state of one conversation.
export type TurnLifecycle =
  | { name: 'idle' }
  | { name: 'starting' }
  | { name: 'running'; runID: string; stage: string }
  | { name: 'finished' }
  | { name: 'failed'; error?: string };

export const isTurnBusy = (turn: TurnLifecycle) =>
  turn.name === 'starting' || turn.name === 'running';

// ConversationState is the live UI state of one conversation. Each
// conversation owns its transcript, turn state, permission mode,
// think level, and pending prompts, so turns in different
// conversations can run in parallel.
export interface ConversationState {
  content: ConversationContent;
  turn: TurnLifecycle;
  messages: MessageView[];
  // turnArtifacts keeps one entry per turn (start = index of the
  // turn's first message in messages), each with the files that turn
  // produced. Live turns fill in via "artifact" events; resumed
  // sessions rebuild the list from the per-turn archive.
  turnArtifacts: TurnArtifacts[];
  mode: string;
  think: string;
  model: string;
  pendingInteracts: InteractDTO[];
}

export type ToastKind = 'info' | 'warning';

export interface ToastItem {
  id: number;
  text: string;
  kind: ToastKind;
}

let msgSeq = 0;
const newID = (prefix: string) => `${prefix}-${Date.now()}-${msgSeq++}`;
let turnSeq = 0;
const newTurnID = () => `live-${++turnSeq}`;

// MAX_CONV_MESSAGES bounds the in-memory transcript per conversation.
// Older messages stay in the backend archive (sessionTurns) and can be
// re-opened via resume; keeping them in the store would grow memory
// without bound on long-running sessions.
const MAX_CONV_MESSAGES = 800;

// capConversation trims the oldest messages past the in-memory cap and
// re-bases per-turn artifact strip indexes onto the trimmed array.
function capConversation(conv: ConversationState): ConversationState {
  if (conv.messages.length <= MAX_CONV_MESSAGES) return conv;
  const drop = conv.messages.length - MAX_CONV_MESSAGES;
  const messages = conv.messages.slice(drop);
  const turnArtifacts = conv.turnArtifacts
    .map((t) => ({ ...t, start: t.start - drop }))
    .filter((t) => t.start >= 0);
  return { ...conv, messages, turnArtifacts };
}

// normalizeArgs coerces the wire form of tool arguments to a string:
// arguments is a json.RawMessage, so the frontend receives a parsed
// object/array rather than text, and rendering it raw crashes React.
function normalizeArgs(args: unknown): string {
  if (typeof args === 'string') return args;
  try {
    return JSON.stringify(args, null, 2);
  } catch {
    return String(args);
  }
}

const emptyConv = (): ConversationState => ({
  content: { name: 'empty' },
  turn: { name: 'idle' },
  messages: [],
  turnArtifacts: [],
  mode: 'workspace',
  think: 'medium',
  model: '',
  pendingInteracts: [],
});

const errorMessage = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

// TurnDoc is one file produced by the current turn, reported by the
// backend's workspace observer ("artifact" UI event).
export interface TurnDoc {
  path: string;
  bytes: number;
}

// TurnArtifacts is one turn's produced files plus the index of its
// first message in the flattened transcript.
export interface TurnArtifacts {
  id: string;
  start: number;
  docs: TurnDoc[];
  // requestedAt is when the user's message was accepted; startedAt is
  // when agent execution began; finishedAt/durationMs cover the run.
  // They are set live and restored from the per-turn archive on resume.
  requestedAt?: string;
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
  // runID is set once the live turn starts, so post-turn artifact
  // reconciliation ("artifact_sync") can target exactly this turn.
  runID?: string;
}

// attachmentPart lowers one staged attachment into the message wire
// form: images/audio/video become URL-sourced media parts (the backend
// persists them and the prepare hook inlines the bytes), anything else
// becomes a file part.
function attachmentPart(a: AttachmentView): StreamPart {
  if (a.kind === 'image') {
    return {
      type: 'image',
      source: { kind: 'url', url: a.path, media_type: a.media_type },
    };
  }
  if (a.kind === 'audio') {
    return {
      type: 'audio',
      source: { kind: 'url', url: a.path, media_type: a.media_type },
    };
  }
  if (a.kind === 'video') {
    return {
      type: 'video',
      source: { kind: 'url', url: a.path, media_type: a.media_type },
    };
  }
  return { type: 'file', uri: a.path, name: a.name, media_type: a.media_type };
}

const baseName = (p: string) => p.split(/[\\/]/).pop() ?? p;

// historyPartsToAttachments extracts media parts from an archived user
// message into renderable attachments. The archive keeps URL-form
// sources (local paths under the session's media/files dirs).
function historyPartsToAttachments(parts: HistoryPart[]): AttachmentView[] {
  const out: AttachmentView[] = [];
  for (const p of parts) {
    if (p.type === 'image' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'image',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'audio' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'audio',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'video' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'video',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'file' && p.uri) {
      out.push({
        id: newID('att'),
        kind: 'file',
        path: p.uri,
        name: p.name || baseName(p.uri),
        media_type: p.media_type,
      });
    }
  }
  return out;
}

// historyToMessages converts stored flowcraft messages back into the
// live MessageView shape: user text, then assistant messages with the
// same ordered blocks (reasoning, tool calls, text) the stream
// produces. Text-bearing assistant replies keep their own row so a
// long history stays cheap to render; tool-only rounds (which produce
// no visible separator) are appended to the previous assistant row so
// resumed sessions do not show a stack of repeated tool group cards.
const historyToMessages = (history: HistoryMessage[]): MessageView[] => {
  const messages: MessageView[] = [];
  const toolCalls: {
    item: Extract<AssistantItem, { kind: 'tool_call' }>;
  }[] = [];
  for (const h of history) {
    const parts = h.content?.parts ?? [];
    if (h.role === 'user') {
      const text = parts
        .filter((p): p is { type: 'text'; text?: string } => p.type === 'text')
        .map((p) => p.text ?? '')
        .join('');
      messages.push({
        id: newID('msg'),
        role: 'user',
        text,
        items: [],
        attachments: historyPartsToAttachments(parts),
      });
      continue;
    }
    if (h.role === 'tool') {
      for (const p of parts) {
        if (p.type !== 'tool_result' || !p.result) continue;
        const call = toolCalls.find(
          (c) => c.item.tool.id === p.result!.call_id,
        );
        if (call) {
          call.item.tool.status = p.result.is_error ? 'error' : 'done';
          call.item.tool.result = sanitizeToolResult(p.result.content ?? '');
        }
      }
      continue;
    }
    const hasVisibleText = parts.some(
      (p) => p.type === 'text' && Boolean((p as { text?: string }).text),
    );
    let msg = messages[messages.length - 1];
    if (!msg || msg.role !== 'assistant' || hasVisibleText) {
      msg = {
        id: newID('msg'),
        role: 'assistant',
        text: '',
        items: [],
        attachments: [],
      };
      messages.push(msg);
    }
    for (const p of parts) {
      switch (p.type) {
        case 'text':
          if (p.text) {
            msg.items.push({ kind: 'text', id: newID('part'), text: p.text });
          }
          break;
        case 'reasoning':
          if (p.text) {
            msg.items.push({
              kind: 'reasoning',
              id: newID('part'),
              text: p.text,
            });
          }
          break;
        case 'tool_call': {
          const call = p.call;
          if (!call) break;
          const item: Extract<AssistantItem, { kind: 'tool_call' }> = {
            kind: 'tool_call',
            id: newID('part'),
            tool: {
              id: call.id,
              name: call.name,
              args: normalizeArgs(call.arguments),
              status: 'running',
            },
          };
          msg.items.push(item);
          toolCalls.push({ item });
          break;
        }
      }
    }
    messages.push(msg);
  }
  return messages;
};

// historyTurnsToState rebuilds the transcript and per-turn artifact
// groups from the archived per-turn records, so resuming renders one
// artifact strip under each turn's messages.
function historyTurnsToState(turns: SessionTurn[]): {
  messages: MessageView[];
  turnArtifacts: TurnArtifacts[];
} {
  const messages: MessageView[] = [];
  const turnArtifacts: TurnArtifacts[] = [];
  for (const turn of turns) {
    const start = messages.length;
    messages.push(...historyToMessages(turn.messages));
    turnArtifacts.push({
      id: `h-${turn.seq}`,
      start,
      requestedAt: turn.requested_at || turn.at,
      startedAt: turn.started_at || turn.at,
      finishedAt: turn.finished_at || turn.at,
      durationMs: turn.duration_ms,
      docs: (turn.artifacts ?? []).map((a) => ({
        path: a.path,
        bytes: a.bytes ?? 0,
      })),
    });
  }
  return { messages, turnArtifacts };
}

// lastAssistant returns a mutable copy of the last assistant message
// (creating one when needed) plus a NEW messages array, so every
// stream delta produces fresh references and React re-renders.
function lastAssistant(messages: MessageView[]): {
  msg: MessageView;
  messages: MessageView[];
} {
  const last = messages[messages.length - 1];
  if (!last || last.role !== 'assistant') {
    const msg: MessageView = {
      id: newID('msg'),
      role: 'assistant',
      text: '',
      items: [],
      attachments: [],
    };
    return { msg, messages: [...messages, msg] };
  }
  const msg = { ...last, items: [...last.items] };
  return { msg, messages: [...messages.slice(0, -1), msg] };
}

function mergeAppend(
  msg: MessageView,
  kind: 'text' | 'reasoning',
  text: string,
) {
  const items = msg.items;
  const lastItem = items[items.length - 1];
  if (lastItem && lastItem.kind === kind) {
    msg.items = [
      ...items.slice(0, -1),
      { ...lastItem, text: lastItem.text + text },
    ];
  } else {
    msg.items = [...items, { kind, id: newID('part'), text }];
  }
}

// friendlyInterruption maps engine interruption errors to user-facing
// text so raw engine internals (e.g. "engine: interrupted
// (host_shutdown)") never leak into the transcript. It returns null
// when the error is not an interruption, so the original error stays.
function friendlyInterruption(error: string): string | null {
  const m = error.match(/^engine: interrupted(?: \(([a-z_]+)\))?(?:: (.+))?$/);
  if (!m) return null;
  const cause = m[1] ?? '';
  const detail = m[2];
  switch (cause) {
    case 'host_shutdown':
      return i18n.t('chat.interruptedHostShutdown');
    case 'user_cancel':
      return i18n.t('chat.cancelled');
    case 'user_input':
      return i18n.t('chat.interruptedUserInput');
    case 'custom':
      return detail ?? i18n.t('chat.interrupted');
    default:
      return i18n.t('chat.interrupted');
  }
}

// mergeTurnDoc appends a produced file, or refreshes its byte count in
// place when the same path is written again.
function mergeTurnDoc(docs: TurnDoc[], path: string, bytes: number): TurnDoc[] {
  const idx = docs.findIndex((d) => d.path === path);
  const entry: TurnDoc = { path, bytes };
  if (idx < 0) return [...docs, entry];
  return [...docs.slice(0, idx), entry, ...docs.slice(idx + 1)];
}

// applyStream folds one stream delta into a message list and returns
// the new list (immutable).
function applyStream(
  messages: MessageView[],
  delta: StreamDelta,
): MessageView[] {
  if (delta.type !== 'part' || !delta.part) return messages;
  const part = delta.part;
  switch (part.type) {
    case 'text': {
      const text = part.text ?? '';
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages);
      mergeAppend(msg, 'text', text);
      return next;
    }
    case 'reasoning': {
      const text = part.text ?? '';
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages);
      mergeAppend(msg, 'reasoning', text);
      return next;
    }
    case 'tool_call': {
      const { msg, messages: next } = lastAssistant(messages);
      msg.items = [
        ...msg.items,
        {
          kind: 'tool_call',
          id: newID('part'),
          tool: {
            id: part.call.id,
            name: part.call.name,
            args: normalizeArgs(part.call.arguments),
            status: 'running',
          },
        },
      ];
      return next;
    }
    case 'tool_result': {
      const id = part.result.call_id;
      let next = messages;
      for (let i = 0; i < messages.length; i++) {
        const m = messages[i];
        if (m.role !== 'assistant') continue;
        let changed = false;
        const updatedItems = m.items.map((item) => {
          if (item.kind !== 'tool_call' || item.tool.id !== id) return item;
          changed = true;
          return {
            ...item,
            tool: {
              ...item.tool,
              status: part.result.is_error
                ? ('error' as const)
                : ('done' as const),
              result: sanitizeToolResult(part.result.content ?? ''),
            },
          };
        });
        if (changed) {
          next = [
            ...messages.slice(0, i),
            { ...m, items: updatedItems },
            ...messages.slice(i + 1),
          ];
          break;
        }
      }
      return next;
    }
    default:
      return messages;
  }
}

interface StoreState {
  status: ConfigStatus | null;
  configured: boolean;
  fatal: string | null;
  configOpen: boolean;
  configTab: string;
  toolsView: ToolPage | null;
  workspace: string;
  agents: AgentSummary[];
  sessions: SessionMeta[];
  automations: AutomationTask[];
  automationRuns: Record<string, AutomationRun[]>;
  navigation: SessionNavigation;
  conversations: Record<string, ConversationState>;
  runConvs: Record<string, string>;
  // subagentStreams folds live stream deltas of delegated subagent
  // runs (keyed by run id) for the SubagentSidebar; they never render
  // into a chat conversation. subagentStreamAt tracks the last delta
  // timestamp so stale runs can be pruned once their kanban card is
  // gone.
  subagentStreams: Record<string, MessageView[]>;
  subagentStreamAt: Record<string, number>;
  // composerDraft is a one-shot draft injected into the chat composer
  // (used by the automations "create with OpenCraft" flow).
  composerDraft: string;
  statusText: string;
  lastUsage: UsageDTO | null;
  cards: KanbanCard[];
  subagentCards: KanbanCard[];
  subagentPanelOpen: boolean;
  modelOptions: ModelOption[];
  theme: 'dark' | 'light' | 'auto';
  workspaces: WorkspaceMeta[];
  toasts: ToastItem[];
  sessionsLoading: boolean;

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  send: (text: string, attachments?: AttachmentView[]) => Promise<void>;
  retryLast: () => Promise<void>;
  clearLastFailed: () => void;
  replyInteract: (id: string, req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openConfig: (tab?: string) => void;
  closeConfig: () => void;
  openTools: (view: ToolPage) => void;
  closeTools: () => void;
  newChat: () => Promise<void>;
  resume: (id: string) => Promise<void>;
  deleteSession: (id: string) => Promise<void>;
  setMode: (mode: string) => Promise<void>;
  setThink: (level: string) => Promise<void>;
  setModel: (model: string) => Promise<void>;
  setTheme: (theme: 'dark' | 'light' | 'auto') => void;
  loadWorkspaces: () => Promise<void>;
  chooseWorkspace: () => Promise<void>;
  openWorkspace: (path: string) => Promise<void>;
  removeWorkspace: (id: string) => Promise<void>;
  draftComposer: (text: string) => void;
  clearComposerDraft: () => void;
  refreshAgents: () => Promise<void>;
  loadSessions: () => Promise<void>;
  loadAutomations: () => Promise<void>;
  loadAutomationRuns: (taskId: string) => Promise<void>;
  loadCards: () => Promise<void>;
  loadSubagentCards: () => Promise<void>;
  toggleSubagentPanel: () => void;
  flash: (text: string) => void;
  toast: (text: string, kind?: ToastKind) => void;
  dismissToast: (id: number) => void;
}

let themeMedia: MediaQueryList | null = null;
let themeMediaHandler: (() => void) | null = null;

// applyTheme resolves dark/light/auto (auto follows the OS preference)
// and keeps a media-query listener alive while auto is selected.
function applyTheme(theme: 'dark' | 'light' | 'auto') {
  const mq = window.matchMedia('(prefers-color-scheme: dark)');
  const resolved = theme === 'auto' ? (mq.matches ? 'dark' : 'light') : theme;
  document.documentElement.classList.toggle(
    'theme-light',
    resolved === 'light',
  );
  if (theme === 'auto') {
    if (themeMedia === mq) return;
    themeMedia?.removeEventListener('change', themeMediaHandler!);
    themeMedia = mq;
    themeMediaHandler = () => applyTheme('auto');
    mq.addEventListener('change', themeMediaHandler);
  } else {
    themeMedia?.removeEventListener('change', themeMediaHandler!);
    themeMedia = null;
    themeMediaHandler = null;
  }
}

export const useStore = create<StoreState>((set, get) => {
  let toastSeq = 0;
  let navigationSeq = 0;
  // Session switches must land on the backend in the same order the
  // user requested them. Without this queue, an older resumeSession
  // can finish after a newer NewChat and move the backend context back
  // to the old session.
  let contextSwitchQueue: Promise<void> = Promise.resolve();
  const runContextSwitch = <T>(op: () => Promise<T>) => {
    const next = contextSwitchQueue.then(op);
    contextSwitchQueue = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  };
  const updateConv = (id: string, patch: Partial<ConversationState>) =>
    set((state) => {
      const conv = state.conversations[id];
      if (!conv) return state;
      return {
        conversations: {
          ...state.conversations,
          [id]: capConversation({ ...conv, ...patch }),
        },
      };
    });

  const updateNavigation = (action: SessionNavigationAction) =>
    set((state) => ({
      navigation: navigationReducer(state.navigation, action),
    }));

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
            content: { name: 'live-shell' },
            turn: { name: 'starting' },
          },
        },
      }));
      conv = get().conversations[convID];
    }
    return conv;
  };

  const beginTurn = async (
    convID: string,
    text: string,
    messages: MessageView[],
    attachments: AttachmentView[] = [],
  ) => {
    const conv = get().conversations[convID];
    const requestedAt = new Date().toISOString();
    // Keep completed turns' strips; trim anything that started at or
    // beyond the (possibly retried) transcript, then open a new live
    // turn entry at the user message just appended.
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
      content: { name: 'ready' },
      messages,
      turnArtifacts,
      turn: { name: 'starting' },
    });
    try {
      const parts: StreamPart[] = [];
      if (text) parts.push({ type: 'text', text });
      for (const att of attachments) {
        parts.push(attachmentPart(att));
      }
      const wire: TurnMessage = { role: 'user', content: { parts } };
      const start = await api.startTurn(convID, wire);
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
      set((state) => ({
        runConvs: { ...state.runConvs, [start.run_id]: convID },
        conversations: {
          ...state.conversations,
          [convID]: {
            ...state.conversations[convID],
            turn: {
              name: 'running',
              runID: start.run_id,
              stage: '',
            },
            turnArtifacts,
          },
        },
      }));
      void get().loadSessions();
    } catch (err) {
      const conv = get().conversations[convID];
      if (conv) {
        const { msg, messages: next } = lastAssistant(conv.messages);
        mergeAppend(msg, 'text', `⛔ ${String(err)}`);
        updateConv(convID, {
          messages: next,
          turn: { name: 'failed', error: String(err) },
        });
      }
    }
  };

  return {
    status: null,
    configured: false,
    fatal: null,
    configOpen: false,
    configTab: 'ui',
    toolsView: null,
    workspace: '',
    agents: [],
    sessions: [],
    automations: [],
    automationRuns: {},
    navigation: { name: 'idle', epoch: 0 },
    conversations: {},
    runConvs: {},
    subagentStreams: {},
    subagentStreamAt: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    cards: [],
    subagentCards: [],
    subagentPanelOpen: true,
    modelOptions: [],
    theme: 'dark',
    workspaces: [],
    toasts: [],
    sessionsLoading: false,

    init: async () => {
      const saved = window.localStorage.getItem('opencraft.theme');
      const theme = saved === 'light' || saved === 'auto' ? saved : 'dark';
      applyTheme(theme);
      set({ theme });
      try {
        const [
          status,
          workspace,
          mode,
          currentSession,
          think,
          model,
          modelOptions,
        ] = await Promise.all([
          api.configStatus(),
          api.workspace(),
          api.sessionMode(),
          api.currentSession(),
          api.getThink(),
          api.getModel(),
          api.modelOptions(),
        ]);
        set({
          status,
          workspace,
          configured: !status.needed,
          configOpen: false,
          toolsView: null,
          navigation:
            currentSession !== ''
              ? { name: 'ready', sessionID: currentSession, epoch: 0 }
              : { name: 'idle', epoch: 0 },
          conversations: {
            ...(currentSession !== ''
              ? {
                  [currentSession]: {
                    ...emptyConv(),
                    mode,
                    think,
                    model,
                  },
                }
              : {}),
          },
          modelOptions,
          theme,
        });
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
      switch (ev.type) {
        case 'ready': {
          const data = ev.data as ConfigStatus;
          const workChanged = data.work_dir !== get().workspace;
          if (workChanged) {
            // Workspace switched: start fresh in the new workspace.
            updateNavigation({
              type: 'idle',
              epoch: ++navigationSeq,
            });
            set({
              workspace: data.work_dir,
              conversations: {},
              runConvs: {},
              subagentStreams: {},
              subagentStreamAt: {},
            });
            void get().loadSessions();
            // Mint a fresh backend conversation so the app-level
            // conversationID follows the new workspace.
            void get().newChat();
          }
          // Reload the session list on every ready event, not only
          // when the workspace changed: a backend that finishes
          // assembling after the frontend already ran its initial
          // load would otherwise leave the list empty until the first
          // turn triggers a refresh.
          void get().loadSessions();
          void get().loadAutomations();
          set((state) => ({
            status: data,
            configured: !data.needed,
            configOpen: workChanged ? false : state.configOpen,
            fatal: null,
          }));
          void api.modelOptions().then((modelOptions) => set({ modelOptions }));
          void get().refreshAgents();
          void get().loadWorkspaces();
          break;
        }
        case 'fatal':
          set({ fatal: (ev.data as { error: string }).error ?? '' });
          break;
        case 'stream': {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            delta: StreamDelta;
          };
          if (!data.run_id) break;
          const convID = get().runConvs[data.run_id] || data.conversation_id;
          if (convID) {
            // Main turn: fold into its own conversation.
            const conv = ensureConversation(convID);
            if (conv) {
              const part = data.delta?.part;
              const stage =
                part?.type === 'reasoning'
                  ? 'reasoning'
                  : part?.type === 'tool_call'
                    ? `tool:${part.call.name}`
                    : part?.type === 'text'
                      ? 'text'
                      : '';
              const turn =
                conv.turn.name === 'running'
                  ? { ...conv.turn, stage }
                  : conv.turn.name === 'starting' && data.run_id
                    ? { name: 'running' as const, runID: data.run_id, stage }
                    : conv.turn;
              updateConv(convID, {
                messages: applyStream(conv.messages, data.delta),
                turn,
              });
            }
            break;
          }
          // Delegated subagent run (inherited observer sink): fold
          // into the sidebar stream, never into a chat.
          const runID = data.run_id;
          set((state) => ({
            subagentStreams: {
              ...state.subagentStreams,
              [runID]: applyStream(
                state.subagentStreams[runID] ?? [],
                data.delta,
              ),
            },
            subagentStreamAt: {
              ...state.subagentStreamAt,
              [runID]: Date.now(),
            },
          }));
          break;
        }
        case 'status':
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case 'usage':
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case 'interact': {
          const spec = ev.data as InteractDTO;
          const convID =
            (spec.run_id && get().runConvs[spec.run_id]) ||
            spec.conversation_id;
          if (!convID) break;
          const conv = ensureConversation(convID);
          if (!conv) break;
          if (!conv.pendingInteracts.some((p) => p.id === spec.id)) {
            updateConv(convID, {
              pendingInteracts: [...conv.pendingInteracts, spec],
            });
          }
          break;
        }
        case 'resolved': {
          const id = (ev.data as { id: string }).id;
          for (const [convID, conv] of Object.entries(get().conversations)) {
            if (conv.pendingInteracts.some((p) => p.id === id)) {
              updateConv(convID, {
                pendingInteracts: conv.pendingInteracts.filter(
                  (p) => p.id !== id,
                ),
              });
              break;
            }
          }
          break;
        }
        case 'artifact': {
          const data = ev.data as {
            conversation_id?: string;
            path?: string;
            bytes?: number;
          };
          const convID = data.conversation_id;
          if (!convID || !data.path) break;
          const conv = ensureConversation(convID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          if (list.length === 0) break;
          const idx = list.length - 1;
          const docs = mergeTurnDoc(list[idx].docs, data.path, data.bytes ?? 0);
          updateConv(convID, {
            turnArtifacts: [
              ...list.slice(0, idx),
              { ...list[idx], docs },
              ...list.slice(idx + 1),
            ],
          });
          break;
        }
        case 'artifact_sync': {
          // Post-turn reconciliation: the backend merged exec-produced
          // documents into the turn archive; replace that turn's docs
          // with the authoritative list.
          const data = ev.data as {
            conversation_id?: string;
            run_id?: string;
            artifacts?: { path: string; bytes?: number }[];
          };
          const convID = data.conversation_id;
          if (!convID || !Array.isArray(data.artifacts)) break;
          const conv = ensureConversation(convID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          const idx = list.findIndex((t) => t.runID && t.runID === data.run_id);
          if (idx < 0) break;
          const docs = data.artifacts.map((a) => ({
            path: a.path,
            bytes: a.bytes ?? 0,
          }));
          updateConv(convID, {
            turnArtifacts: [
              ...list.slice(0, idx),
              { ...list[idx], docs },
              ...list.slice(idx + 1),
            ],
          });
          break;
        }
        case 'turn_end': {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            status: string;
            error?: string;
            finished_at?: string;
          };
          // Unknown-run terminations (subagent turns) must not settle a
          // main conversation's turn; only tracked main turns are handled.
          const convID =
            (data.run_id && get().runConvs[data.run_id]) ||
            data.conversation_id;
          if (!convID) break;
          const conv = ensureConversation(convID);
          if (!conv) break;
          const failed =
            data.status === 'failed' ||
            data.status === 'aborted' ||
            data.status === 'canceled' ||
            data.status === 'interrupted';
          let messages = conv.messages;
          const note = failed
            ? data.status === 'canceled'
              ? i18n.t('chat.cancelled')
              : data.status === 'interrupted'
                ? i18n.t('chat.interrupted')
                : i18n.t('chat.failed')
            : '';
          if (data.error || (failed && note)) {
            const friendly = data.error
              ? friendlyInterruption(data.error)
              : null;
            const { msg, messages: next } = lastAssistant(messages);
            mergeAppend(
              msg,
              'text',
              `\n\n> ⛔ ${friendly ?? data.error ?? note}`,
            );
            messages = next;
          }
          const finishedAt = data.finished_at || new Date().toISOString();
          set((state) => {
            const runConvs = { ...state.runConvs };
            delete runConvs[data.run_id ?? ''];
            const conv = state.conversations[convID];
            if (!conv) return state;
            const turnArtifacts = conv.turnArtifacts.map((t) =>
              t.runID && t.runID === data.run_id ? { ...t, finishedAt } : t,
            );
            return {
              runConvs,
              conversations: {
                ...state.conversations,
                [convID]: capConversation({
                  ...conv,
                  messages,
                  turnArtifacts,
                  turn: failed
                    ? { name: 'failed', error: data.error }
                    : { name: 'finished' },
                }),
              },
            };
          });
          void get().loadSessions();
          break;
        }
        case 'session_updated':
          void get().loadSessions();
          break;
        case 'automation_changed':
          void get().loadAutomations();
          break;
        case 'automation_run': {
          const data = ev.data as AutomationRun;
          void get().loadAutomations();
          if (data?.task_id) void get().loadAutomationRuns(data.task_id);
          break;
        }
        case 'automation_run_started': {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
          };
          if (data.conversation_id) {
            // The run targets the currently open workspace: create a
            // live shell when needed and register the run route so
            // stream/turn_end events settle this conversation.
            ensureConversation(data.conversation_id);
            if (data.run_id) {
              set((state) => ({
                conversations: {
                  ...state.conversations,
                  [data.conversation_id!]: {
                    ...(state.conversations[data.conversation_id!] ?? {
                      ...emptyConv(),
                      content: { name: 'live-shell' },
                    }),
                    turn: {
                      name: 'running',
                      runID: data.run_id!,
                      stage: '',
                    },
                  },
                },
                runConvs: {
                  ...state.runConvs,
                  [data.run_id!]: data.conversation_id!,
                },
              }));
            }
          }
          break;
        }
        case 'managed_restored': {
          // The settings save rolled plugin-owned provider edits back
          // to the stored config; surface the reminder so the silent
          // restore is visible.
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

    send: async (text, attachments = []) => {
      const trimmed = text.trim();
      const state = get();
      const convID = activeSessionID(state.navigation);
      const conv = convID ? state.conversations[convID] : undefined;
      if (
        (!trimmed && attachments.length === 0) ||
        !convID ||
        !conv ||
        isTurnBusy(conv.turn) ||
        !state.configured
      ) {
        return;
      }
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
      await beginTurn(convID, trimmed, messages, attachments);
    },

    retryLast: async () => {
      const state = get();
      const convID = activeSessionID(state.navigation);
      const conv = convID ? state.conversations[convID] : undefined;
      if (!convID || !conv || isTurnBusy(conv.turn)) return;
      let lastUserIdx = -1;
      for (let i = conv.messages.length - 1; i >= 0; i--) {
        if (conv.messages[i].role === 'user') {
          lastUserIdx = i;
          break;
        }
      }
      if (lastUserIdx < 0) return;
      const text = conv.messages[lastUserIdx].text;
      const attachments = conv.messages[lastUserIdx].attachments ?? [];
      await beginTurn(
        convID,
        text,
        conv.messages.slice(0, lastUserIdx + 1),
        attachments,
      );
    },

    clearLastFailed: () => {
      const convID = activeSessionID(get().navigation);
      const conv = convID ? get().conversations[convID] : undefined;
      if (conv?.turn.name === 'failed') {
        updateConv(convID, { turn: { name: 'idle' } });
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
      for (const [convID, conv] of Object.entries(get().conversations)) {
        if (conv.pendingInteracts.some((p) => p.id === id)) {
          updateConv(convID, {
            pendingInteracts: conv.pendingInteracts.filter((p) => p.id !== id),
          });
          break;
        }
      }
    },

    cancelRun: async () => {
      const convID = activeSessionID(get().navigation);
      const conv = convID ? get().conversations[convID] : undefined;
      if (conv?.turn.name === 'running') {
        try {
          await api.cancelTurn(conv.turn.runID);
        } catch {
          // turn_end settles the UI regardless
        }
      }
    },

    openConfig: (tab) => set({ configOpen: true, configTab: tab ?? 'ui' }),
    closeConfig: () => set({ configOpen: false }),

    openTools: (view) => set({ toolsView: view, configOpen: false }),
    closeTools: () => set({ toolsView: null }),

    newChat: async () => {
      const previous = get().navigation;
      try {
        const nav = ++navigationSeq;
        updateNavigation({
          type: 'switch',
          kind: 'new',
          previousSessionID:
            previous.name === 'ready' ? previous.sessionID : undefined,
          epoch: nav,
        });
        const snapshot = await runContextSwitch(() => api.newChat());
        const id = snapshot.session_id;
        if (nav !== navigationSeq) return;
        set((state) => ({
          navigation: navigationReducer(state.navigation, {
            type: 'ready',
            sessionID: id,
            epoch: nav,
          }),
          toolsView: null,
          subagentStreams: {},
          subagentStreamAt: {},
          conversations: {
            ...state.conversations,
            [id]: emptyConv(),
          },
        }));
      } catch (err) {
        const nav = get().navigation;
        updateNavigation({
          type: 'fail',
          epoch: nav.name === 'switching' ? nav.epoch : navigationSeq,
          previousSessionID:
            previous.name === 'ready' ? previous.sessionID : undefined,
          error: errorMessage(err),
        });
      }
      void get().loadSessions();
    },

    resume: async (id) => {
      const previous = get().navigation;
      try {
        const nav = ++navigationSeq;
        updateNavigation({
          type: 'switch',
          kind: 'resume',
          targetID: id,
          previousSessionID:
            previous.name === 'ready' ? previous.sessionID : undefined,
          epoch: nav,
        });
        // Switch the backend session context first (conversation id,
        // mode, think, model all follow the selected session); without
        // this the mode/think/model reads below return the previous
        // conversation's values and new turns land in the old session.
        const snapshot = await runContextSwitch(() => api.resumeSession(id));
        if (nav !== navigationSeq) return;
        const resolvedID = snapshot.session_id;
        const existing = get().conversations[resolvedID];
        if (existing?.content.name === 'ready') {
          set({
            navigation: navigationReducer(get().navigation, {
              type: 'ready',
              sessionID: resolvedID,
              epoch: nav,
            }),
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
          return;
        }
        const turns = await api.sessionTurns(resolvedID);
        if (nav !== navigationSeq) return;
        const { messages, turnArtifacts } = historyTurnsToState(turns);
        // A live shell may already hold the current run's streamed
        // messages. Keep them after the archived history; completed
        // shells are replaced by the archive instead of duplicated.
        const keepLive =
          existing?.content.name === 'live-shell' &&
          (existing.turn.name === 'running' ||
            existing.turn.name === 'starting');
        const mergedMessages = keepLive
          ? [...messages, ...existing.messages]
          : messages;
        set((state) => ({
          navigation: navigationReducer(state.navigation, {
            type: 'ready',
            sessionID: resolvedID,
            epoch: nav,
          }),
          toolsView: null,
          conversations: {
            ...state.conversations,
            [resolvedID]: capConversation({
              ...emptyConv(),
              content: { name: 'ready' },
              mode: snapshot.mode,
              think: snapshot.think,
              model: snapshot.model,
              messages: mergedMessages,
              turnArtifacts,
            }),
          },
        }));
      } catch (err) {
        const nav = get().navigation;
        updateNavigation({
          type: 'fail',
          epoch: nav.name === 'switching' ? nav.epoch : navigationSeq,
          previousSessionID:
            previous.name === 'ready' ? previous.sessionID : undefined,
          error: errorMessage(err),
        });
      }
    },

    deleteSession: async (id) => {
      try {
        await api.deleteSession(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          return { conversations };
        });
        if (activeSessionID(get().navigation) === id) {
          // The active conversation is gone: switch to a fresh one so
          // the chat never points at a deleted session.
          await get().newChat();
        }
        await get().loadSessions();
      } catch (err) {
        const message = String(err);
        if (/cannot delete the active conversation/i.test(message)) {
          get().toast(i18n.t('sidebar.cannotDeleteActive'), 'warning');
        } else {
          set({ statusText: message });
        }
      }
    },

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        const convID = activeSessionID(get().navigation);
        if (convID) updateConv(convID, { mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setThink: async (level) => {
      try {
        await api.setThink(level);
        const convID = activeSessionID(get().navigation);
        if (convID) updateConv(convID, { think: level });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setModel: async (model) => {
      try {
        await api.setModel(model);
        const convID = activeSessionID(get().navigation);
        if (convID) updateConv(convID, { model });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setTheme: (theme) => {
      applyTheme(theme);
      window.localStorage.setItem('opencraft.theme', theme);
      set({ theme });
    },

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
        set({ sessions: (await api.listSessions()) ?? [] });
      } catch {
        // best-effort
      } finally {
        set({ sessionsLoading: false });
      }
    },

    loadAutomations: async () => {
      try {
        set({ automations: (await api.automations()) ?? [] });
      } catch {
        // best-effort
      }
    },

    loadAutomationRuns: async (taskId: string) => {
      try {
        const list = (await api.automationRuns(taskId)) ?? [];
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
      try {
        set({ workspaces: (await api.workspaces()) ?? [] });
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

    openWorkspace: async (path) => {
      try {
        await api.openWorkspace(path);
        // The runtime rebuild emits "ready", which resets the
        // conversations and refreshes sessions/workspaces.
      } catch (err) {
        set({ statusText: String(err) });
      }
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

    loadCards: async () => {
      try {
        set({ cards: (await api.delegationCards()) ?? [] });
      } catch {
        // best-effort
      }
    },

    loadSubagentCards: async () => {
      const convID = activeSessionID(get().navigation);
      if (!convID) return;
      try {
        const cards = (await api.conversationDelegationCards(convID)) ?? [];
        set((state) => {
          // Auto-show the panel when the conversation starts spawning
          // subagents; a manual close sticks while cards are present.
          const open =
            state.subagentPanelOpen ||
            (cards.length > 0 && state.subagentCards.length === 0);
          // Skip re-render when nothing changed (2s polling otherwise
          // sets a fresh array every tick).
          const same =
            state.subagentCards.length === cards.length &&
            cards.every(
              (c, i) =>
                state.subagentCards[i]?.id === c.id &&
                state.subagentCards[i]?.status === c.status &&
                state.subagentCards[i]?.updated_at === c.updated_at,
            );
          if (same && state.subagentPanelOpen === open) return state;
          // Prune sidebar streams whose run is no longer on any card
          // and has been silent for a while (claim lag is seconds).
          const cardRuns = new Set(
            cards.map((c) => c.run_id).filter(Boolean) as string[],
          );
          const now = Date.now();
          const subagentStreams = { ...state.subagentStreams };
          const subagentStreamAt = { ...state.subagentStreamAt };
          for (const runID of Object.keys(subagentStreams)) {
            if (
              !cardRuns.has(runID) &&
              now - (subagentStreamAt[runID] ?? 0) > 60_000
            ) {
              delete subagentStreams[runID];
              delete subagentStreamAt[runID];
            }
          }
          return {
            subagentCards: cards,
            subagentPanelOpen: open,
            subagentStreams,
            subagentStreamAt,
          };
        });
      } catch {
        // best-effort; the sidebar polls again shortly
      }
    },

    toggleSubagentPanel: () =>
      set((state) => ({ subagentPanelOpen: !state.subagentPanelOpen })),

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
