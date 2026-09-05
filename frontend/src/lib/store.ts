import { create } from 'zustand';
import i18n from '../i18n';
import { api } from './api';
import { sanitizeToolResult } from './ansi';
import { coalesceStreamEvents } from './stream';
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
  SessionDefaults,
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
import { routeBackendEvent, type EventDataSink } from '../state/eventRouter';
import { stateRoot } from '../state/app';

export interface ToolView {
  id: string;
  name: string;
  args: string;
  status: 'running' | 'done' | 'error';
  result?: string;
}

// AssistantItem preserves the stream arrival order of one assistant
// block (reasoning trace, tool call, or text), so renderers can show
// output in the exact order the model produced it. Reasoning traces
// are hidden from the chat transcript.
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

// ConversationState is the live UI state of one conversation. Each
// conversation owns its transcript, turn state, permission mode,
// think level, and pending prompts, so turns in different
// conversations can run in parallel.
export interface ConversationState {
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

// pendingConversationIDs returns the unique conversations that own at
// least one pending interact/prompt. The Sidebar uses this instead of
// iterating every loaded conversation on each store update.
export function pendingConversationIDs(
  pendingPromptConvs: Record<string, string>,
): string[] {
  return [...new Set(Object.values(pendingPromptConvs))];
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

// STREAM_FLUSH_MAX_DELAY_MS bounds how long a burst of stream deltas
// can stay queued when animation frames are paused (for example while
// the webview is occluded). The rAF flush normally runs at frame rate;
// this timer guarantees a commit still happens even when rAF is not
// being driven.
const STREAM_FLUSH_MAX_DELAY_MS = 50;

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

const emptyConv = (
  over?: Partial<Pick<ConversationState, 'mode' | 'think' | 'model'>>,
): ConversationState => ({
  messages: [],
  turnArtifacts: [],
  mode: over?.mode ?? 'workspace',
  think: over?.think ?? 'medium',
  model: over?.model ?? '',
  pendingInteracts: [],
});

// firstMessageTitle mirrors the backend's archive-title fallback: a
// conversation displays the first line of its first user message until
// a real title (manual rename or the LLM auto-title) replaces it.
// New sessions have no stored record while their first turn is still
// running, so the running row and chat header use this local copy.
export function firstMessageTitle(messages: MessageView[]): string {
  for (const m of messages) {
    if (m.role !== 'user') continue;
    const text = m.text.trim();
    if (text) {
      const line = text.split('\n', 1)[0].trim();
      const runes = Array.from(line);
      if (runes.length <= 70) return line;
      return `${runes.slice(0, 70).join('')}…`;
    }
    if (m.attachments.length > 0) return '[attachment]';
  }
  return '';
}

const errorMessage = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

// TurnDoc is one file produced by the current turn, reported by the
// backend's workspace observer ("artifact" UI event).
export interface TurnDoc {
  path: string;
  bytes: number;
}

// TurnStatus is the persisted terminal state of an archived turn. It
// mirrors the backend turn_end status so resumed sessions can render
// failures/cancellations without embedding an error into message text.
export type TurnStatus =
  'completed' | 'failed' | 'aborted' | 'canceled' | 'interrupted';

// TurnArtifacts is one turn's produced files plus the index of its
// first message in the flattened transcript.
export interface TurnArtifacts {
  id: string;
  start: number;
  docs: TurnDoc[];
  // requestedAt is when the user's message was accepted; startedAt is
  // when agent execution began; finishedAt/durationMs cover the run.
  // durationMs comes from the backend turn_end event or archive and is
  // preferred over timestamps when rendering "worked for".
  // They are set live and restored from the per-turn archive on resume.
  requestedAt?: string;
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
  // runID is set once the live turn starts, so post-turn artifact
  // reconciliation ("artifact_sync") can target exactly this turn.
  runID?: string;
  // status/error come from the live turn_end event and are persisted
  // with the archived turn for resumed sessions.
  status?: TurnStatus;
  error?: string;
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
    const cleaned = stripLegacyTurnMarker(historyToMessages(turn.messages));
    messages.push(...cleaned.messages);
    turnArtifacts.push({
      id: `h-${turn.seq}`,
      start,
      runID: turn.run_id,
      requestedAt: turn.requested_at || turn.at,
      startedAt: turn.started_at || turn.at,
      finishedAt: turn.finished_at || turn.at,
      durationMs: turn.duration_ms,
      status: normalizeTurnStatus(turn.status) ?? cleaned.status,
      error: turn.error,
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

function normalizeTurnStatus(status?: string): TurnStatus | undefined {
  switch (status) {
    case 'completed':
    case 'failed':
    case 'aborted':
    case 'canceled':
    case 'interrupted':
      return status;
    default:
      return undefined;
  }
}

// stripLegacyTurnMarker removes the old `> ⛔/⏹/⚠️` text that older
// versions appended inside assistant messages. New turns persist
// status/error on the archive row instead, so the transcript stays
// clean; the legacy marker still lets us recover a status for history
// that predates the archive column.
function stripLegacyTurnMarker(messages: MessageView[]): {
  messages: MessageView[];
  status?: TurnStatus;
} {
  let status: TurnStatus | undefined;
  const next: MessageView[] = [];
  for (const msg of messages) {
    let changed = false;
    const items: AssistantItem[] = [];
    for (const item of msg.items) {
      if (item.kind !== 'text') {
        items.push(item);
        continue;
      }
      const m = item.text.match(/(?:\n\n)?>\s*(⏹|⚠️|⛔)\s+[\s\S]*$/);
      if (!m) {
        items.push(item);
        continue;
      }
      status =
        m[1] === '⛔' ? 'failed' : m[1] === '⏹' ? 'canceled' : 'interrupted';
      const text = item.text.slice(0, m.index ?? 0).trimEnd();
      if (text) items.push({ ...item, text });
      changed = true;
    }
    if (changed && items.length === 0) continue;
    next.push(changed ? { ...msg, items } : msg);
  }
  return { messages: next, status };
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
export function friendlyInterruption(error: string): string | null {
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

// friendlyFailure maps flowcraft graph/inference errors to user-safe
// text. The `graph "..." node "..."` prefix is internal plumbing; a
// provider failure only needs to tell the user the model call did not
// go through and that retrying is reasonable.
export function friendlyFailure(error: string): string | null {
  const m = error.match(
    /^(?:graph "[^"]+" node "[^"]+": )?([a-z_]+)(?: during [a-z_]+)?(?: at [^:]+)?$/,
  );
  if (!m) return null;
  switch (m[1]) {
    case 'provider_failure':
      return i18n.t('chat.providerFailure');
    case 'invalid_provider_response':
      return i18n.t('chat.invalidProviderResponse');
    case 'unknown_provider':
    case 'unknown_model':
    case 'unknown_profile':
      return i18n.t('chat.modelConfiguration');
    case 'invalid_request':
      return i18n.t('chat.invalidRequest');
    default:
      return i18n.t('chat.genericFailure');
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
  conversations: Record<string, ConversationState>;
  runConvs: Record<string, string>;
  // pendingPromptConvs maps a pending interact/prompt id to the
  // conversation that owns it, so routing and the sidebar never scan
  // every conversation on each store update.
  pendingPromptConvs: Record<string, string>;
  // composerDraft is a one-shot draft injected into the chat composer
  // (used by the automations "create with OpenCraft" flow).
  composerDraft: string;
  statusText: string;
  lastUsage: UsageDTO | null;
  cards: KanbanCard[];
  modelOptions: ModelOption[];
  sessionDefaults: SessionDefaults;
  yoloOnly: boolean;
  theme: 'dark' | 'light' | 'auto';
  workspaces: WorkspaceMeta[];
  toasts: ToastItem[];
  sessionsLoading: boolean;

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  flushStreams: () => void;
  send: (text: string, attachments?: AttachmentView[]) => Promise<void>;
  forkTurn: (runID: string) => Promise<void>;
  clearLastFailed: () => void;
  replyInteract: (id: string, req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openConfig: (tab?: string) => void;
  closeConfig: () => void;
  openTools: (view: ToolPage) => void;
  closeTools: () => void;
  newChat: () => Promise<void>;
  resume: (id: string) => Promise<void>;
  retryTranscript: (id: string) => Promise<void>;
  backFromFailure: () => void;
  deleteSession: (id: string) => Promise<void>;
  setMode: (mode: string) => Promise<void>;
  setThink: (level: string) => Promise<void>;
  setModel: (model: string) => Promise<void>;
  setTheme: (theme: 'dark' | 'light' | 'auto') => void;
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
  loadCards: () => Promise<void>;
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
  let pendingStreamEvents: UIEvent[] = [];
  let streamFlushRAF: number | null = null;
  let streamFlushTimer: ReturnType<typeof setTimeout> | null = null;
  // Session switches must land on the backend in the same order the
  // user requested them. Without this queue, an older resumeSession
  // can finish after a newer NewChat and move the backend context back
  // to the old session.
  let contextSwitchQueue: Promise<void> = Promise.resolve();
  // Workspace switches are applied in order. Older restores ignore
  // their result once a newer switch has been requested.
  let workspaceSwitchSeq = 0;
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

  const beginTurn = async (
    convID: string,
    text: string,
    messages: MessageView[],
    attachments: AttachmentView[] = [],
  ) => {
    const conv = get().conversations[convID];
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
      workspace: get().workspace,
    });
    startingActor?.send({ type: 'SEND_STARTED' });
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
            turnArtifacts,
          },
        },
      }));
      startingActor?.send({ type: 'RUN_STARTED', runID: start.run_id });
      void get().loadSessions();
    } catch (err) {
      const conv = get().conversations[convID];
      if (conv) {
        startingActor?.send({
          type: 'TURN_ENDED',
          runID: '',
          status: 'failed',
          error: String(err),
        });
      }
    }
  };

  const eventDataSink: EventDataSink = {
    writeConversationData: (conversationID, ev) => {
      switch (ev.type) {
        case 'stream': {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            delta: StreamDelta;
          };
          if (!data.run_id) break;
          const actor = stateRoot.registry.get(conversationID);
          if (actor) {
            const snapshot = actor.getSnapshot();
            const value = snapshot.value as unknown as { turn: string };
            const context = snapshot.context as {
              currentRunID?: string;
              lastEndedRunID?: string;
            };
            if (value.turn === 'running') {
              // A delayed delta from an earlier run must never be
              // folded into a newer run's live transcript.
              if (
                context.currentRunID &&
                data.run_id !== context.currentRunID
              ) {
                break;
              }
            } else if (
              (value.turn === 'starting' || value.turn === 'idle') &&
              data.run_id === context.lastEndedRunID
            ) {
              // The machine already ignores a stream for the last ended
              // run; mirror it here so data never reaches the renderer.
              break;
            }
          }
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          updateConv(conversationID, {
            messages: applyStream(conv.messages, data.delta),
          });
          break;
        }
        case 'interact': {
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
        case 'resolved': {
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
        case 'artifact': {
          const data = ev.data as {
            path?: string;
            bytes?: number;
          };
          if (!data.path) break;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          if (list.length === 0) break;
          const idx = list.length - 1;
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
        case 'artifact_sync': {
          const data = ev.data as {
            run_id?: string;
            artifacts?: { path: string; bytes?: number }[];
          };
          if (!Array.isArray(data.artifacts)) break;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          const idx = list.findIndex((t) => t.runID && t.runID === data.run_id);
          if (idx < 0) break;
          const docs = data.artifacts.map((a) => ({
            path: a.path,
            bytes: a.bytes ?? 0,
          }));
          updateConv(conversationID, {
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
            status: string;
            error?: string;
            finished_at?: string;
            duration_ms?: number;
          };
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const finishedAt = data.finished_at || new Date().toISOString();
          set((state) => {
            const runConvs = { ...state.runConvs };
            delete runConvs[data.run_id ?? ''];
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
                  }
                : t,
            );
            return {
              runConvs,
              conversations: {
                ...state.conversations,
                [conversationID]: capConversation({
                  ...conv,
                  turnArtifacts,
                }),
              },
            };
          });
          void get().loadSessions();
          break;
        }
        case 'automation_run_started': {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
          };
          const conv = get().conversations[conversationID];
          if (!conv) {
            set((state) => ({
              conversations: {
                ...state.conversations,
                [conversationID]: emptyConv(),
              },
            }));
          }
          if (data.run_id) {
            set((state) => ({
              runConvs: {
                ...state.runConvs,
                [data.run_id!]: conversationID,
              },
            }));
          }
          break;
        }
      }
    },

    writeGlobalData: (ev) => {
      switch (ev.type) {
        case 'ready': {
          const data = ev.data as ConfigStatus;
          const workChanged = data.work_dir !== get().workspace;
          if (workChanged) {
            // Keep conversation actors and transcripts alive across a
            // workspace switch: a turn that is still running in the
            // old workspace continues to stream into its conversation.
            // Only the focus is reset; the session restore below
            // decides which conversation the new workspace shows.
            stateRoot.sendFocus({ type: 'WORKSPACE_RESET' });
            set({
              workspace: data.work_dir,
              toolsView: null,
              configOpen: false,
            });
            void get().loadSessions();
            const restoreFor = suppressRestoreFor;
            suppressRestoreFor = null;
            if (restoreFor !== data.work_dir) {
              const restore = get().restoreWorkspaceSession(data.work_dir);
              workspaceRestoreInFlight = true;
              workspaceRestorePromise = restore;
              void restore.finally(() => {
                if (workspaceRestorePromise === restore) {
                  workspaceRestorePromise = null;
                  workspaceRestoreInFlight = false;
                }
              });
            }
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
        case 'fatal':
          set({ fatal: (ev.data as { error: string }).error ?? '' });
          break;
        case 'status':
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case 'usage':
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case 'managed_restored': {
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
    refreshAutomations: () => void get().loadAutomations(),
    refreshAutomationRuns: (ev) => {
      const data = ev.data as AutomationRun;
      if (data?.task_id) void get().loadAutomationRuns(data.task_id);
    },
    conversationForRunID: (runID) => get().runConvs[runID],
    pendingInteractConversation: (promptID) =>
      get().pendingPromptConvs[promptID],
    activeWorkspace: () => get().workspace,
  };

  const activeConversationID = () => {
    const snapshot = stateRoot.focusSnapshot;
    return snapshot.value === 'active'
      ? (snapshot.context as { sessionID: string }).sessionID
      : '';
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
    };
    switch (value.turn) {
      case 'starting':
        return { name: 'starting' as const };
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
    for (const ev of coalesceStreamEvents(events)) {
      routeBackendEvent(ev, { root: stateRoot, data: eventDataSink });
    }
  };

  const scheduleStreamFlush = () => {
    if (streamFlushRAF !== null || streamFlushTimer !== null) return;
    const flush = () => flushPendingStreams();
    streamFlushRAF =
      typeof requestAnimationFrame === 'function'
        ? requestAnimationFrame(flush)
        : null;
    streamFlushTimer = setTimeout(flush, STREAM_FLUSH_MAX_DELAY_MS);
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
      const mappedRun = Object.values(state.runConvs).includes(id);
      const hasPendingPrompt = conv.pendingInteracts.length > 0;
      if (running || mappedRun || hasPendingPrompt) {
        conversations[id] = conv;
      } else {
        dropped.push(id);
      }
    }
    if (dropped.length === 0) return;
    set(() => ({ conversations }));
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
        return {
          conversations: {
            ...stateNow.conversations,
            [conversationID]: capConversation({
              ...conv,
              messages: [...conv.messages.slice(0, start), ...rebuilt.messages],
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

  return {
    status: null,
    configured: false,
    fatal: null,
    configOpen: false,
    configTab: 'general',
    toolsView: null,
    workspace: '',
    agents: [],
    sessions: [],
    automations: [],
    automationRuns: {},
    conversations: {},
    runConvs: {},
    pendingPromptConvs: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    cards: [],
    modelOptions: [],
    sessionDefaults: { mode: 'workspace', think: 'medium' },
    yoloOnly: false,
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
        set({
          status,
          workspace,
          configured: !status.needed,
          configOpen: false,
          toolsView: null,
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
          // A binding double may resolve without the new fields; fall
          // back to the canonical defaults. A genuinely missing
          // binding rejects the batch above and surfaces as fatal
          // with a retry path (shell and backend ship together).
          sessionDefaults: defaults ?? { mode: 'workspace', think: 'medium' },
          yoloOnly: profile?.yolo_only ?? false,
          theme,
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
            .sessionTurns(currentSession)
            .then((turns) => {
              const { messages, turnArtifacts } = historyTurnsToState(turns);
              set((state) => ({
                conversations: {
                  ...state.conversations,
                  [currentSession]: capConversation({
                    ...(state.conversations[currentSession] ?? emptyConv()),
                    messages,
                    turnArtifacts,
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
      if (ev.type === 'stream') {
        pendingStreamEvents.push(ev);
        scheduleStreamFlush();
        return;
      }
      // Non-stream events are ordering boundaries: any buffered deltas
      // must land before the terminal/global event that follows them.
      flushPendingStreams();
      const turnEndData =
        ev.type === 'turn_end'
          ? (ev.data as { run_id?: string; conversation_id?: string })
          : undefined;
      const turnEndConversationID =
        turnEndData?.conversation_id ??
        (turnEndData?.run_id ? get().runConvs[turnEndData.run_id] : undefined);
      routeBackendEvent(ev, { root: stateRoot, data: eventDataSink });
      if (turnEndConversationID) {
        void reconcileTurnFromArchive(
          turnEndConversationID,
          turnEndData?.run_id ?? '',
        );
      }
    },

    flushStreams: () => flushPendingStreams(),

    send: async (text, attachments = []) => {
      const trimmed = text.trim();
      const state = get();
      const convID = activeConversationID();
      const conv = convID ? state.conversations[convID] : undefined;
      if (
        (!trimmed && attachments.length === 0) ||
        !convID ||
        !conv ||
        (() => {
          const turn = conversationTurnState(convID);
          return turn.name === 'starting' || turn.name === 'running';
        })() ||
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
      if (turn?.name === 'running') {
        try {
          await api.cancelTurn(turn.runID);
        } catch (err) {
          // Surface cancel failures instead of leaving the UI running
          // silently; a real cancel still settles via turn_end.
          set({ statusText: String(err) });
        }
      }
    },

    openConfig: (tab) => set({ configOpen: true, configTab: tab ?? 'general' }),
    closeConfig: () => set({ configOpen: false }),

    openTools: (view) => set({ toolsView: view, configOpen: false }),
    closeTools: () => set({ toolsView: null }),

    newChat: async () => {
      stateRoot.sendFocus({ type: 'OPEN_NEW' });
      const request = stateRoot.focusSnapshot.context.request;
      try {
        const snapshot = await runContextSwitch(() => api.newChat());
        stateRoot.sendFocus({
          type: 'OPEN_SUCCEEDED',
          request,
          sessionID: snapshot.session_id,
        });
        const focus = stateRoot.focusSnapshot;
        if (
          focus.value !== 'active' ||
          focus.context.sessionID !== snapshot.session_id
        ) {
          return;
        }
        const id = snapshot.session_id;
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

    retryTranscript: async (id) => {
      const actor = stateRoot.registry.ensure(id, {
        workspaceGeneration: stateRoot.generation(),
        workspace: get().workspace,
      });
      const context = actor?.getSnapshot().context as {
        lastHydrateRequest?: number;
      };
      const request = (context?.lastHydrateRequest ?? 0) + 1;
      const generation = stateRoot.generation();
      actor?.send({ type: 'HYDRATE_REQUESTED', request, generation });
      try {
        const turns = await api.sessionTurns(id);
        const { messages, turnArtifacts } = historyTurnsToState(turns);
        set((state) => ({
          conversations: {
            ...state.conversations,
            [id]: capConversation({
              ...emptyConv(),
              mode: state.conversations[id]?.mode ?? 'workspace',
              think: state.conversations[id]?.think ?? 'medium',
              model: state.conversations[id]?.model ?? '',
              messages,
              turnArtifacts,
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

    resume: async (id) => {
      if (activeConversationID() === id) {
        // Returning to the already-active conversation means closing
        // whatever overlay/tool page currently covers the chat.
        set({ toolsView: null, configOpen: false });
        return;
      }
      stateRoot.sendFocus({ type: 'OPEN_SESSION', id });
      const request = stateRoot.focusSnapshot.context.request;
      try {
        const snapshot = await runContextSwitch(() => api.resumeSession(id));
        stateRoot.sendFocus({
          type: 'OPEN_SUCCEEDED',
          request,
          sessionID: snapshot.session_id,
        });
        const focus = stateRoot.focusSnapshot;
        if (
          focus.value !== 'active' ||
          focus.context.sessionID !== snapshot.session_id
        ) {
          return;
        }
        const resolvedID = snapshot.session_id;
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
          turns = await api.sessionTurns(resolvedID);
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
        const { messages, turnArtifacts } = historyTurnsToState(turns);
        // A live shell may already hold the current run's streamed
        // messages. Keep them after the archived history; completed
        // shells are replaced by the archive instead of duplicated.
        const keepLive =
          Boolean(existing) &&
          (actorValue?.turn === 'running' || actorValue?.turn === 'starting');
        const mergedMessages = keepLive
          ? [...messages, ...existing.messages]
          : messages;
        set((state) => ({
          toolsView: null,
          conversations: {
            ...state.conversations,
            [resolvedID]: capConversation({
              ...emptyConv(),
              mode: snapshot.mode,
              think: snapshot.think,
              model: snapshot.model,
              messages: mergedMessages,
              turnArtifacts,
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
    },

    deleteSession: async (id) => {
      const deleteOnce = async () => {
        try {
          await api.deleteSession(id);
        } catch (err) {
          const message = String(err);
          // New chats mint lazily: after the UI switches to an unsent
          // draft the backend still tracks the previous conversation as
          // current, so deleting it is refused. Promote the draft to a
          // fresh (discarded) backend conversation first — but never
          // bypass the guard while the session still has a live turn.
          if (
            /cannot delete the active conversation/i.test(message) &&
            stateRoot.focusSnapshot.value === 'no-session'
          ) {
            const turn = conversationTurnState(id);
            if (turn.name !== 'starting' && turn.name !== 'running') {
              await api.newChat();
              await api.deleteSession(id);
              return;
            }
          }
          throw err;
        }
      };
      try {
        // Settle any queued deltas before deleting so a late flush
        // cannot resurrect the conversation after the tombstone.
        flushPendingStreams();
        await deleteOnce();
        stateRoot.registry.get(id)?.send({
          type: 'SESSION_DELETED',
          deletedAt: new Date().toISOString(),
        });
        stateRoot.registry.markDeleted(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          return { conversations };
        });
        clearPendingIndex(id);
        if (activeConversationID() === id) {
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
        // sessions/workspaces.
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    openSessionInWorkspace: async (sessionID, workspacePath) => {
      const state = get();
      if (workspacePath && workspacePath !== state.workspace) {
        await api.openWorkspace(workspacePath);
        // The backend emits "ready" asynchronously relative to the
        // binding response; wait until the store has applied it so a
        // pending newChat is queued before resume tries to open the
        // target session in the new workspace.
        const deadline = Date.now() + 5000;
        while (get().workspace !== workspacePath && Date.now() < deadline) {
          await new Promise((resolve) => setTimeout(resolve, 20));
        }
        if (get().workspace !== workspacePath) {
          throw new Error('workspace switch did not complete');
        }
        await waitForWorkspaceRestore();
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
          const deadline = Date.now() + 8000;
          while (get().workspace !== target && Date.now() < deadline) {
            await new Promise((resolve) => setTimeout(resolve, 20));
          }
          if (get().workspace !== target) {
            throw new Error('workspace switch did not complete');
          }
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

    loadCards: async () => {
      try {
        set({ cards: (await api.delegationCards()) ?? [] });
      } catch {
        // best-effort
      }
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
