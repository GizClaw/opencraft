import { UIEventType } from './events';
import { create } from 'zustand';
import i18n from '../i18n';
import { api } from './api';
import {
  applyUISettings,
  cacheUISettings,
  readCachedUISettings,
  DEFAULT_UI_SETTINGS,
  type UISettings,
} from './appearance';
import { sanitizeToolResult } from './ansi';
import { COMPACT_SUMMARY_PREFIX } from './compact';
import { followLinkTarget } from './linkTarget';
import { measureInteraction, scheduleFlushCommit } from './perfMetrics';
import { coalesceStreamEvents, streamFlushInterval } from './stream';
import { toolResultImages, toolResultText, type ToolImage } from './toolresult';
import type {
  AgentSummary,
  AutomationRun,
  AutomationTask,
  AttachmentView,
  ConfigStatus,
  DelegationNote,
  FileTab,
  InteractDTO,
  HistoryPart,
  HistoryMessage,
  ModelOption,
  ReplyRequest,
  ResolvedTarget,
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
  // seenAt / endedAt are this client's clock for one call: stamped when
  // the tool_call arrives and when its result lands. They stay
  // undefined for calls replayed from the archive, which carries no
  // per-call timing, so a card never renders an invented duration.
  seenAt?: number;
  endedAt?: number;
  // images keeps the inline image parts a tool returned (view_image is
  // the only built-in that produces them), so the card can show what
  // the model saw instead of a caption about it.
  images?: ToolImage[];
}

// FileViewerState is the memory-only file viewer state of one
// conversation. Visibility, tabs and tree position are all scoped to
// the session, so switching chats restores exactly that chat's panel.
export interface FileViewerState {
  filesOpen: boolean;
  // panelMode picks which right-rail view is active: the file browser
  // or the repository Git panel.
  panelMode: 'files' | 'git';
  // gitPick is a one-shot handoff from the viewer's marks chip to the
  // Git panel: the repo-relative path to reveal, plus a nonce so the
  // same pick can be handed over twice without the panel re-opening on
  // every refresh. The panel consumes it (consumeGitPick) so switching
  // segments later does not re-open the same diff.
  gitPick?: { path: string; nonce: number };
  fileTabs: FileTab[];
  fileActive: string | null;
  fileTreeDir: string;
}

function viewerDefaults(): FileViewerState {
  return {
    filesOpen: false,
    panelMode: 'files',
    fileTabs: [] as FileTab[],
    fileActive: null as string | null,
    fileTreeDir: '.',
  };
}

// gitPickNonce numbers the viewer → Git panel handoffs. A counter (not
// a timestamp) so two clicks in the same millisecond stay distinct.
let gitPickNonce = 0;

// AssistantItem preserves the stream arrival order of one assistant
// block (reasoning trace, tool call, or text), so renderers can show
// output in the exact order the model produced it. Reasoning traces
// are hidden from the chat transcript.
// TextItemFields is the shared body of a streamed text/reasoning block.
// While the block is still streaming its body lives in chunks: appending
// a delta is then an array push instead of a copy of the whole answer,
// and the per-item cap can drop the oldest chunks without touching the
// rest. When the block ends (a tool call, the turn end) the chunks are
// folded back into text.
export interface TextItemFields {
  id: string;
  text: string;
  chunks?: string[];
}

// AssistantItem preserves stream order; the two text kinds stay separate
// union members so `Extract<AssistantItem, { kind: 'text' }>` — the
// pattern the renderers use to pick text blocks — keeps working.
export type AssistantItem =
  | ({ kind: 'reasoning' } & TextItemFields)
  | ({ kind: 'text' } & TextItemFields)
  | { kind: 'tool_call'; id: string; tool: ToolView };

// TextItem is either text kind, for helpers that treat them alike.
type TextItem = ({ kind: 'reasoning' } | { kind: 'text' }) & TextItemFields;

// itemText returns the display text of a streaming block, joining the
// accumulated chunks. Callers that render or copy the text use this
// instead of reading item.text directly.
export function itemText(item: { text: string; chunks?: string[] }): string {
  if (!item.chunks || item.chunks.length === 0) return item.text;
  if (item.chunks.length === 1) return item.chunks[0];
  return item.chunks.join('');
}

export interface MessageView {
  id: string;
  role: 'user' | 'assistant';
  // text carries the user message body; assistant messages render
  // their ordered items instead.
  text: string;
  items: AssistantItem[];
  // droppedItems counts the oldest blocks a live turn folded away once
  // the message hit MAX_ITEMS_PER_MESSAGE. The transcript renders the
  // count so the beginning of a very long turn is not silently gone; the
  // archive still holds every block.
  droppedItems?: number;
  // attachments renders user message media: images above the bubble,
  // other files in a floating list below it.
  attachments: AttachmentView[];
  // steer marks a user row that was interjected into a running turn
  // instead of opening one. It is the row's whole lifecycle, so the
  // transcript can render the state the row is in rather than a bubble
  // that reads like a new turn:
  //
  //   pending      handed to the run, not picked up at a round
  //                boundary yet;
  //   delivered    a boundary took it, and the archive has it too (a
  //                resumed session tags archived rows the same way);
  //   undelivered  the turn ended without taking it. Nothing appended
  //                the text to the conversation, so this row is the
  //                only copy and it keeps its resend/discard actions.
  //
  // Absent on every row that opened its turn, and on rows a resumed
  // session cannot tell apart (see historyToMessages).
  steer?: SteerState;
  // kind and note mark a row the app itself wrote: kind is the archived
  // author ("delegation_note") and note its decoded fields. The
  // transcript renders the card from `note` and never reads such a row
  // as user speech, and a kind whose fields did not decode still marks
  // the row as the app's so it cannot be mistaken for something the
  // user said.
  kind?: string;
  note?: DelegationNote;
}

// SteerState is where a mid-turn interjection stands. See
// MessageView.steer for the three states.
export type SteerState = 'pending' | 'delivered' | 'undelivered';

// QueuedSteer tracks an optimistic steer row handed to a live run, in
// submission order (the engine's queue is FIFO). turn_end maps the
// undelivered count the backend reports (steer_pending) onto the newest
// entries and stamps every tracked row with its final state.
//
// The entries live only until their run's turn_end classifies them. A
// run whose terminal event never arrives (a lost event, a turn
// superseded by a barge-in) leaves its entries behind; the next
// turn_end in that conversation settles those rows as undelivered, so
// no row claims delivery is still coming for a run that is gone.
export interface QueuedSteer {
  runID: string;
  messageID: string;
  text: string;
}

// carryUndeliveredSteers picks the interjection rows a turn ended
// without delivering and appends them to a transcript rebuilt from the
// archive. They are not in the archive (nothing appended their text to
// the conversation), so carrying them over is what keeps the only copy
// of the words; dropping them would drop the text.
//
// A row whose text the rebuilt transcript already holds as a delivered
// interjection is dropped instead: that shape comes from a boundary
// that did take the message while the run's terminal event was lost, so
// the settle-on-drop below guessed wrong and the archive is the truth.
function carryUndeliveredSteers(
  messages: MessageView[] | undefined,
  rebuilt: MessageView[],
): MessageView[] {
  const delivered = new Set(
    rebuilt.filter((m) => m.steer === 'delivered').map((m) => m.text),
  );
  return (messages ?? []).filter(
    (m) => m.steer === 'undelivered' && !delivered.has(m.text),
  );
}

// capUndeliveredSteers bounds the number of undelivered steer rows one
// conversation keeps. Each one is the only copy of its text; a
// conversation that keeps producing them should still not grow without
// bound, so the oldest are dropped (the same policy the old card list
// used).
function capUndeliveredSteers(messages: MessageView[]): MessageView[] {
  let count = 0;
  for (const m of messages) {
    if (m.steer === 'undelivered') count += 1;
  }
  if (count <= MAX_UNDELIVERED_STEERS) return messages;
  let drop = count - MAX_UNDELIVERED_STEERS;
  const dropped = new Set<string>();
  for (const m of messages) {
    if (drop === 0) break;
    if (m.steer === 'undelivered') {
      dropped.add(m.id);
      drop -= 1;
    }
  }
  return messages.filter((m) => !dropped.has(m.id));
}

// settleSteerDelivery marks the interjection rows a round boundary has
// taken as delivered, without waiting for the run's turn_end. The
// engine's steer queue is FIFO and the backend reports how many of a
// run's messages are still waiting (steer_pending), so the oldest
// (sent - waiting) rows are the ones the boundary carried into the
// conversation.
//
// The rows left waiting keep their pending state: the live count says
// nothing about why a message is still queued, and only the turn's end
// classifies what never made it (see the turn_end handler). The rows
// this settles leave steerSent, which is exactly what that handler
// reconciles.
function settleSteerDelivery(
  conv: ConversationState,
  runID: string,
  pending: number,
): Partial<ConversationState> | null {
  const tracked = (conv.steerSent ?? []).filter((s) => s.runID === runID);
  const taken = tracked.length - Math.max(0, Math.floor(pending));
  if (taken <= 0) return null;
  const ids = new Set(tracked.slice(0, taken).map((s) => s.messageID));
  return {
    messages: conv.messages.map((m) =>
      ids.has(m.id) ? { ...m, steer: 'delivered' } : m,
    ),
    steerSent: (conv.steerSent ?? []).filter((s) => !ids.has(s.messageID)),
  };
}

// QueuedInput is the single draft a user staged while a turn is
// starting or running. interrupt=false is the Tab queue: it fires
// after the watched turn completes. interrupt=true is a barge-in send
// (Cmd/Ctrl+Enter) that arrived before the awaited run had a run id,
// so it fires as soon as that run starts (and barges it in).
export interface QueuedInput {
  text: string;
  attachments: AttachmentView[];
  interrupt: boolean;
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
  queued?: QueuedInput;
  // historySeq is the smallest archived turn seq currently loaded and
  // historyHasMore says whether older turns still exist in the archive.
  // Startup hydration only pulls the newest page, so scrolling to the top
  // asks the backend for the turns just below historySeq instead of the
  // whole session. historyLoading guards concurrent page requests.
  historySeq?: number;
  historyHasMore?: boolean;
  historyLoading?: boolean;
  // steerSent tracks steer rows still awaiting their run's turn_end;
  // the rows themselves carry the state the end stamps on them
  // (MessageView.steer). A row leaves the list once its state is
  // settled, so the list holds exactly the rows a still-running turn was
  // handed, and it is render-process state that starts empty again after
  // a reload. Rows the archive cannot see (undelivered ones) are carried
  // across transcript rebuilds — see carryUndeliveredSteers.
  steerSent?: QueuedSteer[];
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

// HISTORY_PAGE_TURNS / INITIAL_HISTORY_TURNS bound how much of a long
// conversation is pulled from the archive at once. Startup hydration
// takes the newest page only; scrolling above it pages backwards. Both
// requests ask for one turn more than they keep, so the extra turn can
// be dropped while still knowing whether older turns exist.
const INITIAL_HISTORY_TURNS = 6;
const HISTORY_PAGE_TURNS = 10;

// TAIL_SYNC_TURNS bounds one read of the transcript's tail: the turns
// appended after the newest seq the transcript holds are normally one
// app-authored turn (a delegation note), so the cap only exists to keep
// a pathological append burst from crossing the bridge in one payload.
const TAIL_SYNC_TURNS = 20;

// MAX_ITEMS_PER_MESSAGE bounds how many blocks one assistant message
// keeps while a turn streams. A long turn appends a reasoning block, a
// text block and a tool call per round; past the cap the oldest blocks
// fold into a counter, which is what keeps the message (and every
// re-render that walks it) bounded. The archive keeps the full turn.
const MAX_ITEMS_PER_MESSAGE = 400;

// MAX_UNDELIVERED_STEERS bounds the per-conversation list of steered
// messages a turn ended without delivering. Each one is a card in the
// transcript; a conversation that keeps producing them should not grow
// the list without bound.
const MAX_UNDELIVERED_STEERS = 20;

// MAX_REASONING_CHARS keeps only the tail of a reasoning trace: the
// transcript never renders it, so everything it holds is overhead.
const MAX_REASONING_CHARS = 16 << 10;

// MAX_ITEM_TEXT_CHARS caps one visible text block. The tail is what the
// reader is watching while streaming, so head and tail are kept with a
// marker between them; the archive still holds the full text.
const MAX_ITEM_TEXT_CHARS = 256 << 10;

// MAX_TOOL_ARGS_CHARS caps the stored argument text of one tool call.
// write_file carries whole files, and the store keeps args as a compact
// JSON string for the card to parse.
const MAX_TOOL_ARGS_CHARS = 512 << 10;

// MAX_TOOL_RESULT_CHARS caps the text one tool result keeps in the
// store. A result is a JSON envelope a card renders (a file dump, a
// command's output) and nothing re-reads it; the archive keeps the whole
// text, and the head/tail marker says it was cut. Without a bound one
// chatty command wrote megabytes into every re-render of its turn.
const MAX_TOOL_RESULT_CHARS = 256 << 10;

// MAX_CONV_MEDIA_BYTES bounds the inline image payload — base64 data
// URLs, counted in string characters — one conversation keeps in the
// store. Only a single image is capped anywhere else
// (imageutil.MaxInlineImageBytes, 10 MiB): the archive's screenshot-heavy
// conversation holds 84 inline frames (16 MiB) on its own, and a renderer
// that pages back through a few turns of them is where hundreds of
// megabytes of WebKit Malloc came from. An evicted entry keeps its path,
// which is all either viewer needs: AttachmentImage re-reads its preview
// on mount and the view_image card re-reads the file on expand.
const MAX_CONV_MEDIA_BYTES = 16 << 20;

// MAX_CONV_TEXT_BYTES bounds the text one conversation keeps: message
// text, block text, tool arguments and tool results. It is the safety net
// under the per-block caps (MAX_ITEM_TEXT_CHARS, MAX_TOOL_ARGS_CHARS,
// MAX_TOOL_RESULT_CHARS): those bound one block, and a long turn holds up
// to MAX_ITEMS_PER_MESSAGE of them.
const MAX_CONV_TEXT_BYTES = 16 << 20;

// BUDGET_KEEP_MESSAGES is the tail both budgets leave alone. The
// transcript renders the newest turns, so trimming there would be
// visible work on text the reader is looking at; everything older is off
// screen or a scroll away, and getting it back is one fetch.
const BUDGET_KEEP_MESSAGES = 24;

// BUDGET_FOLD_CHARS is the tail one block keeps once the text budget
// folds it: enough for the block to still read as itself.
const BUDGET_FOLD_CHARS = 2 << 10;

// flushDurations keeps the last few stream-flush timings so the optional
// perf probe can report the transcript's per-frame cost without a
// profiler attached. It is a fixed-size ring: no growth, no allocation
// per flush beyond one number.
const FLUSH_SAMPLE_SIZE = 256;
const flushDurations: number[] = [];

// flushTotal counts every flush of the page's lifetime. The ring above can
// only ever say how many timings it kept — its capacity, not activity — so
// the probe diffs this total between reports to get the flushes that
// happened inside each reporting window.
let flushTotal = 0;

function recordFlushDuration(ms: number) {
  flushTotal += 1;
  flushDurations.push(ms);
  if (flushDurations.length > FLUSH_SAMPLE_SIZE) flushDurations.shift();
}

// streamFlushStats summarizes the recorded flush timings: total is the
// page-lifetime count, the percentiles describe the last FLUSH_SAMPLE_SIZE
// flushes (nearest-rank, which is what the probe reports as P50/P95).
export function streamFlushStats(): {
  total: number;
  p50: number;
  p95: number;
  max: number;
} {
  if (flushDurations.length === 0) {
    return { total: flushTotal, p50: 0, p95: 0, max: 0 };
  }
  const sorted = [...flushDurations].sort((a, b) => a - b);
  const pick = (q: number) =>
    sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))];
  return {
    total: flushTotal,
    p50: pick(0.5),
    p95: pick(0.95),
    max: sorted[sorted.length - 1],
  };
}

// capConversation trims the oldest messages past the in-memory cap and
// re-bases per-turn artifact strip indexes onto the trimmed array. The
// cut lands on a turn boundary so a trim never leaves half a turn in
// the store, and the history cursor moves with it: the next "load
// earlier" page continues below the oldest turn still loaded.
function capConversation(conv: ConversationState): ConversationState {
  if (conv.messages.length <= MAX_CONV_MESSAGES) return conv;
  const above = conv.messages.length - MAX_CONV_MESSAGES;
  // Drop whole turns: the largest turn boundary at or below the exact
  // cut. Falling back to the exact cut when the oldest known turn starts
  // past it keeps the cap a real bound; the overshoot in the normal case
  // is one turn, and a mid-turn cut can leave a partial turn whose
  // history cursor would be wrong.
  let drop = 0;
  for (const turn of conv.turnArtifacts) {
    if (turn.start > above) break;
    if (turn.start > drop) drop = turn.start;
  }
  if (drop === 0) drop = above;
  const messages = conv.messages.slice(drop);
  const turnArtifacts = conv.turnArtifacts
    .map((t) => ({ ...t, start: t.start - drop }))
    .filter((t) => t.start >= 0);
  const historySeq = turnArtifacts[0]?.seq ?? conv.historySeq;
  return { ...conv, messages, turnArtifacts, historySeq };
}

// The three helpers below read a message defensively (`?? []`): they run
// on every stream flush, so one row that arrived without an array — an
// old store snapshot, a test fixture, a fixture-shaped archive row —
// must not be able to throw inside the store's write path.

// mediaCharsOf sums the inline image bytes one message holds: attachment
// previews above a user bubble and the image parts of a view_image
// result. Everything else about an attachment is a path and a few
// numbers.
function mediaCharsOf(msg: MessageView): number {
  let total = 0;
  for (const att of msg.attachments ?? []) {
    if (att.data_url) total += att.data_url.length;
  }
  for (const item of msg.items ?? []) {
    if (item.kind !== 'tool_call') continue;
    for (const image of item.tool.images ?? []) total += image.data_url.length;
  }
  return total;
}

// textCharsOf sums the text one message holds. A streaming block is
// counted through its chunk array: joining it here would allocate the
// whole block on every budget check, which is exactly the cost this
// exists to avoid.
function textCharsOf(msg: MessageView): number {
  let total = msg.text?.length ?? 0;
  for (const item of msg.items ?? []) {
    if (item.kind === 'tool_call') {
      total += (item.tool.args?.length ?? 0) + (item.tool.result?.length ?? 0);
      continue;
    }
    total += item.text?.length ?? 0;
    if (item.chunks) {
      for (const chunk of item.chunks ?? []) total += chunk.length;
    }
  }
  return total;
}

// dropMessageMedia clears the inline images of one message, keeping the
// paths. Returns the message unchanged when it held none.
function dropMessageMedia(msg: MessageView): MessageView {
  let changed = false;
  const attachments = (msg.attachments ?? []).map((att) => {
    if (!att.data_url) return att;
    changed = true;
    const { data_url: _dropped, ...rest } = att;
    return rest as AttachmentView;
  });
  const items = (msg.items ?? []).map((item) => {
    if (item.kind !== 'tool_call' || !item.tool.images?.length) return item;
    changed = true;
    return { ...item, tool: { ...item.tool, images: undefined } };
  });
  return changed ? { ...msg, attachments, items } : msg;
}

// foldMessageText folds one message's blocks and tool results down to
// BUDGET_FOLD_CHARS: what the reader sees stays (a marker and the end of
// the text), what the store holds does not.
//
// Tool arguments are deliberately left alone. They are what the cards
// render as structured artifacts (a diff, the body of a file being
// written), and they are not where the bytes are: in the heaviest
// archived conversation the calls' arguments came to 1.7 MiB against
// 23.3 MiB of results. Results are output, and a folded result still
// ends the way it ended.
function foldMessageText(msg: MessageView): MessageView {
  let changed = false;
  const items = (msg.items ?? []).map((item) => {
    if (item.kind === 'tool_call') {
      const tool = item.tool;
      if (tool.result === undefined) return item;
      const result = capChars(tool.result, BUDGET_FOLD_CHARS);
      if (result === tool.result) return item;
      changed = true;
      return { ...item, tool: { ...tool, result } };
    }
    if (item.text.length <= BUDGET_FOLD_CHARS) return item;
    changed = true;
    return capTextItem(foldTextItem(item), BUDGET_FOLD_CHARS);
  });
  return changed ? { ...msg, items } : msg;
}

// settleConversation is the one place a conversation's in-memory size is
// enforced: the message cap first, then the two byte budgets. It is
// called from every write path that can grow a transcript (a stream
// flush, a send, a history page, an archive reconcile), so no path can
// quietly bypass a budget.
//
// opts.cap=false is for the paging write (see loadEarlierHistory): the
// page a reader just scrolled to must not be yanked away by the message
// cap, but the byte budgets still run — paging back through
// screenshot-heavy history is exactly where the media payload grows,
// and it grew unbounded while this path skipped settle altogether.
//
// The budgets are a bound, not a promise: a conversation whose newest
// BUDGET_KEEP_MESSAGES alone exceed one keeps it anyway. Trimming the
// tail would fold text and drop images the reader is looking at, and
// what those bytes are is exactly what the store_media_bytes /
// store_text_bytes probe series exist to show.
function settleConversation(
  conv: ConversationState,
  opts?: { cap?: boolean },
): ConversationState {
  const capped = opts?.cap === false ? conv : capConversation(conv);
  let media = 0;
  let text = 0;
  for (const msg of capped.messages) {
    media += mediaCharsOf(msg);
    text += textCharsOf(msg);
  }
  const overMedia = media > MAX_CONV_MEDIA_BYTES;
  const overText = text > MAX_CONV_TEXT_BYTES;
  if (!overMedia && !overText) return capped;
  // The oldest messages give up their bytes first: dropping a data URL
  // costs a re-fetch when the row is looked at again, folding text costs
  // reading a marker. Media goes first because it is what a screenshot-
  // heavy transcript spends its memory on.
  const keepFrom = Math.max(0, capped.messages.length - BUDGET_KEEP_MESSAGES);
  let changed = false;
  const messages = capped.messages.map((msg, index) => {
    if (index >= keepFrom) return msg;
    let next = msg;
    if (overMedia && media > MAX_CONV_MEDIA_BYTES) {
      const before = mediaCharsOf(next);
      if (before > 0) {
        next = dropMessageMedia(next);
        media -= before;
      }
    }
    if (overText && text > MAX_CONV_TEXT_BYTES) {
      const before = textCharsOf(next);
      const folded = foldMessageText(next);
      if (folded !== next) {
        text -= before - textCharsOf(folded);
        next = folded;
      }
    }
    if (next !== msg) changed = true;
    return next;
  });
  return changed ? { ...capped, messages } : capped;
}

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
    for (const msg of conv.messages) {
      media += mediaCharsOf(msg);
      text += textCharsOf(msg);
    }
  }
  return { media, text, conversations };
}

// normalizeArgs coerces the wire form of tool arguments to a string:
// arguments is a json.RawMessage, so the frontend receives a parsed
// object/array rather than text, and rendering it raw crashes React.
function normalizeArgs(args: unknown): string {
  if (typeof args === 'string') return args;
  try {
    // Compact, not pretty-printed: the card parses this string and
    // pretty-prints it for display itself, so indentation here would only
    // enlarge what the store holds (a written file can be megabytes).
    const raw = JSON.stringify(args);
    if (raw.length > MAX_TOOL_ARGS_CHARS) {
      return `${raw.slice(0, MAX_TOOL_ARGS_CHARS)}…[args truncated]`;
    }
    return raw;
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
  historySeq: 0,
  historyHasMore: false,
});

// firstMessageTitle mirrors the backend's archive-title fallback: a
// conversation displays the first line of its first user message until
// a real title (manual rename or the LLM auto-title) replaces it.
// New sessions have no stored record while their first turn is still
// running, so the running row and chat header use this local copy.
// Rows the app itself wrote (a delegation note) are skipped: they are
// the app speaking to the model, and the backend's own fallback skips
// them the same way.
export function firstMessageTitle(messages: MessageView[]): string {
  for (const m of messages) {
    if (m.role !== 'user' || m.kind) continue;
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
  // seq is the archived turn's sequence number, set when the turn came
  // from the per-turn archive. Paged hydration uses it as the cursor for
  // "load older turns" and to keep the cursor honest after trimming.
  seq?: number;
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
  // runID is set once the live turn starts, so post-turn reconciliation
  // (the archived turn fetched by run id) can target exactly this turn.
  runID?: string;
  // status/error come from the live turn_end event and are persisted
  // with the archived turn for resumed sessions.
  status?: TurnStatus;
  error?: string;
  // interruptCause/errorKind are the structured class of a failed turn
  // (the engine's interrupt cause, the inference error kind). The
  // backend reads them from the engine's typed error, so the UI renders
  // copy without parsing `error`, which is prose flowcraft owns.
  interruptCause?: string;
  errorKind?: string;
  // requestID/responseID are the provider correlation identifiers of
  // the terminal operation: the request id when the provider reported
  // one (usually failures), and the response id once a response
  // started (usually successful turns). The warning box renders them
  // for provider-side correlation.
  requestID?: string;
  responseID?: string;
  // compaction is what automatic context compaction did during this turn
  // (live only): folds rewrite the conversation prefix, so the note
  // explains the cost and context change the transcript cannot show.
  compaction?: {
    folds: number;
    failures?: number;
    notified?: boolean;
  };
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

// historyToMessages converts one archived turn's stored flowcraft
// messages back into the live MessageView shape: user text, then
// assistant messages with the same ordered blocks (reasoning, tool
// calls, text) the stream produces. Text-bearing assistant replies keep
// their own row so a long history stays cheap to render; tool-only
// rounds (which produce no visible separator) are appended to the
// previous assistant row so resumed sessions do not show a stack of
// repeated tool group cards.
//
// A user row past the turn's first one is a steer the turn's boundary
// delivered: the only user messages a turn's archive holds are the ask
// that opened it plus whatever the steer node appended mid-turn (tools
// -> steer -> compact in the assistant graph), and a resume has to keep
// rendering those as interjections instead of as new turns. Two guards
// keep the inference honest: compaction summaries are user-role context
// rows, not the user speaking, and a boundary only appends after a tool
// result, so a steer always follows an assistant round. A batch the
// boundary merged into one message (several parts in one user message)
// comes back as one row: the archive keeps the merge, so the split is
// not recoverable here.
const historyToMessages = (history: HistoryMessage[]): MessageView[] => {
  const messages: MessageView[] = [];
  let sawAsk = false;
  let sawAssistant = false;
  // byCallID indexes the tool calls seen so far so a tool_result is O(1)
  // to attach. Scanning the accumulated list (the old `.find`) made
  // resuming a session quadratic in its number of tool calls, which is
  // exactly the shape of a long turn's archive.
  const byCallID = new Map<
    string,
    Extract<AssistantItem, { kind: 'tool_call' }>
  >();
  for (const h of history) {
    const parts = h.content?.parts ?? [];
    if (h.role === 'user') {
      const text = parts
        .filter((p): p is { type: 'text'; text?: string } => p.type === 'text')
        .map((p) => p.text ?? '')
        .join('');
      let steer: SteerState | undefined;
      if (!text.startsWith(COMPACT_SUMMARY_PREFIX)) {
        if (!sawAsk) {
          sawAsk = true;
        } else if (sawAssistant) {
          steer = 'delivered';
        }
      }
      messages.push({
        id: newID('msg'),
        role: 'user',
        text,
        items: [],
        attachments: historyPartsToAttachments(parts),
        steer,
      });
      continue;
    }
    if (h.role === 'tool') {
      for (const p of parts) {
        if (p.type !== 'tool_result' || !p.result) continue;
        const item = byCallID.get(p.result.call_id);
        if (item) {
          item.tool.status = p.result.is_error ? 'error' : 'done';
          item.tool.result = capToolResult(
            sanitizeToolResult(toolResultText(p.result.content)),
          );
          const images = toolResultImages(p.result.content);
          if (images.length > 0) item.tool.images = images;
        }
      }
      continue;
    }
    sawAssistant = true;
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
            msg.items.push(
              capTextItem({ kind: 'text', id: newID('part'), text: p.text }),
            );
          }
          break;
        case 'reasoning':
          if (p.text) {
            msg.items.push(
              capTextItem({
                kind: 'reasoning',
                id: newID('part'),
                text: p.text,
              }),
            );
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
          byCallID.set(call.id, item);
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
    messages.push(...historyToMessages(turn.messages));
    // A turn the app wrote is marked on its rows: the transcript renders
    // the card from the archived fields, and nothing that asks what the
    // user said reads them as speech. The mark rides on every row of the
    // turn, and the decoded card on the one row that carries the note's
    // text (the note turn holds exactly one message).
    if (turn.kind) {
      for (let i = start; i < messages.length; i++) {
        messages[i].kind = turn.kind;
      }
      const first = messages[start];
      if (first && turn.delegation_note) first.note = turn.delegation_note;
    }
    turnArtifacts.push({
      id: `h-${turn.seq}`,
      start,
      seq: turn.seq,
      runID: turn.run_id,
      requestedAt: turn.requested_at || turn.at,
      startedAt: turn.started_at || turn.at,
      finishedAt: turn.finished_at || turn.at,
      durationMs: turn.duration_ms,
      status: normalizeTurnStatus(turn.status),
      error: turn.error,
      interruptCause: turn.interrupt_cause,
      errorKind: turn.error_kind,
      requestID: turn.request_id,
      responseID: turn.response_id,
      docs: (turn.artifacts ?? []).map((a) => ({
        path: a.path,
        bytes: a.bytes ?? 0,
      })),
    });
  }
  return { messages, turnArtifacts };
}

// historyPage turns one Session.Turns response into conversation state.
// The caller asks for one turn more than the page keeps: the extra turn
// is dropped here, and its presence is what tells us older history
// still exists on the backend.
function historyPage(
  turns: SessionTurn[],
  pageSize: number,
): {
  messages: MessageView[];
  turnArtifacts: TurnArtifacts[];
  historySeq: number;
  historyHasMore: boolean;
} {
  const historyHasMore = turns.length > pageSize;
  const page = historyHasMore ? turns.slice(turns.length - pageSize) : turns;
  const { messages, turnArtifacts } = historyTurnsToState(page);
  return {
    messages,
    turnArtifacts,
    historySeq: page[0]?.seq ?? 0,
    historyHasMore,
  };
}

// newestArchivedSeq is the seq of the newest archived turn the
// transcript holds, and 0 when it holds none (a live-only transcript, or
// one whose archived turns were all trimmed — seq starts at 1).
function newestArchivedSeq(conv: ConversationState): number {
  let newest = 0;
  for (const turn of conv.turnArtifacts) {
    if (turn.seq !== undefined && turn.seq > newest) newest = turn.seq;
  }
  return newest;
}

// foldArchivedTurns folds archive turns into a transcript that is
// already on screen. Placement is by seq — the archive's write order —
// so a note the app wrote while another turn was still running lands
// above that turn's rows, which is where the next full hydrate renders
// it: a turn's row is written when it ends, so its seq is newer than the
// note's. Turns already present are dropped by seq and by run id,
// because the live path may have reconciled the same turn while this
// read was in flight.
//
// Returns undefined when nothing was added, so the caller can skip a
// store write.
function foldArchivedTurns(
  conv: ConversationState,
  fresh: SessionTurn[],
): ConversationState | undefined {
  const knownSeq = new Set<number>();
  const knownRuns = new Set<string>();
  for (const turn of conv.turnArtifacts) {
    if (turn.seq !== undefined) knownSeq.add(turn.seq);
    if (turn.runID) knownRuns.add(turn.runID);
  }
  let messages = conv.messages;
  let turnArtifacts = conv.turnArtifacts;
  let added = false;
  for (const turn of fresh) {
    if (knownSeq.has(turn.seq)) continue;
    if (turn.run_id && knownRuns.has(turn.run_id)) continue;
    const rebuilt = historyTurnsToState([turn]);
    const strip = rebuilt.turnArtifacts[0];
    if (!strip || rebuilt.messages.length === 0) continue;
    let idx = turnArtifacts.findIndex(
      (t) => t.seq !== undefined && t.seq > turn.seq,
    );
    if (idx < 0) idx = turnArtifacts.length;
    const at =
      idx < turnArtifacts.length ? turnArtifacts[idx].start : messages.length;
    const rows = rebuilt.messages.length;
    messages = [
      ...messages.slice(0, at),
      ...rebuilt.messages,
      ...messages.slice(at),
    ];
    turnArtifacts = [
      ...turnArtifacts.slice(0, idx),
      { ...strip, start: at },
      ...turnArtifacts.slice(idx).map((t) => ({ ...t, start: t.start + rows })),
    ];
    knownSeq.add(turn.seq);
    if (turn.run_id) knownRuns.add(turn.run_id);
    added = true;
  }
  if (!added) return undefined;
  return { ...conv, messages, turnArtifacts };
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

function mergeAppend(
  msg: MessageView,
  kind: 'text' | 'reasoning',
  text: string,
) {
  const items = msg.items;
  const lastItem = items[items.length - 1];
  if (lastItem && lastItem.kind === kind) {
    // Append to the block's chunks instead of concatenating the whole
    // answer: the join happens once per render, where the string is
    // needed anyway, and the cap below can drop the oldest chunks.
    const chunks = lastItem.chunks ?? [lastItem.text];
    chunks.push(text);
    msg.items = [
      ...items.slice(0, -1),
      capTextItem({ ...lastItem, chunks, text: '' }),
    ];
  } else {
    msg.items = [...items, capTextItem({ kind, id: newID('part'), text })];
  }
}

// capTextItem enforces the per-block text bound. Reasoning keeps its
// tail (it is never rendered); visible text keeps head and tail with a
// marker in between. The chunk array is trimmed in place, so a block that
// already dropped its oldest chunks is not re-joined on every delta.
// `foldTo` overrides the bound: the conversation text budget folds an old
// message's blocks down to a tail instead of dropping them outright.
function capTextItem<T extends TextItem>(item: T, foldTo?: number): T {
  // Fold the chunks first: the bound is expressed over the whole text.
  const total = itemText(item);
  const limit =
    foldTo ??
    (item.kind === 'reasoning' ? MAX_REASONING_CHARS : MAX_ITEM_TEXT_CHARS);
  if (total.length <= limit) {
    return item;
  }
  if (item.chunks && item.chunks.length > 1) {
    const chunks = item.chunks.slice();
    let size = total.length;
    while (chunks.length > 1 && size > limit) {
      size -= chunks[0].length;
      chunks.shift();
    }
    const marker = `…[trimmed ${total.length - size} chars]`;
    if (chunks.length === 1) {
      const tail = chunks[0].slice(-limit);
      return { ...item, chunks: undefined, text: marker + tail };
    }
    return { ...item, chunks: [marker, ...chunks], text: '' };
  }
  if (item.kind === 'reasoning') {
    return {
      ...item,
      text: `…[trimmed ${total.length - limit} chars]` + total.slice(-limit),
    };
  }
  const head = Math.floor(limit / 2);
  const tail = limit - head;
  return {
    ...item,
    text: `${total.slice(0, head)}\n…[trimmed ${total.length - limit} chars]…\n${total.slice(-tail)}`,
  };
}

// capChars keeps the head and the tail of one long string with a marker
// between them — the shape capTextItem gives a text block, and the same
// one both the per-result cap and the conversation budget use. Neither
// end is arbitrary: a dump says what it is at the top and why it failed
// at the bottom.
function capChars(text: string, limit: number): string {
  if (text.length <= limit) return text;
  const head = Math.floor(limit / 2);
  const tail = limit - head;
  return `${text.slice(0, head)}\n…[trimmed ${text.length - limit} chars]…\n${text.slice(-tail)}`;
}

// capToolResult enforces MAX_TOOL_RESULT_CHARS on one result string.
function capToolResult(text: string): string {
  return capChars(text, MAX_TOOL_RESULT_CHARS);
}

// foldTextItem turns a streaming block's chunks back into one string.
// Called when the block ends (a tool call arrives, the message ends), so
// a settled message holds plain text and no chunk array.
function foldTextItem<T extends TextItem>(item: T): T {
  if (!item.chunks) return item;
  const folded = { ...item, text: itemText(item) };
  delete (folded as TextItem).chunks;
  return folded;
}

// foldLastTextItem folds the trailing text block of a message, if any.
function foldLastTextItem(msg: MessageView) {
  const last = msg.items[msg.items.length - 1];
  if (!last || last.kind === 'tool_call' || !last.chunks) return;
  msg.items = [...msg.items.slice(0, -1), foldTextItem(last)];
}

// trimItems enforces MAX_ITEMS_PER_MESSAGE on one message, folding the
// overflow into droppedItems.
function trimItems(msg: MessageView) {
  if (msg.items.length <= MAX_ITEMS_PER_MESSAGE) return;
  const drop = msg.items.length - MAX_ITEMS_PER_MESSAGE;
  msg.items = msg.items.slice(drop);
  msg.droppedItems = (msg.droppedItems ?? 0) + drop;
}

// friendlyInterruption maps the engine's interrupt cause to user-facing
// text so raw engine internals never leak into the transcript. The cause
// is a field the backend reads from the engine's typed interrupt; the UI
// does not parse the error text. It returns null when the turn did not
// end as an interrupt, so the original error stays.
export function friendlyInterruption(cause?: string): string | null {
  switch (cause) {
    case 'host_shutdown':
      return i18n.t('chat.interruptedHostShutdown');
    case 'user_cancel':
      return i18n.t('chat.cancelled');
    case 'user_input':
      return i18n.t('chat.interruptedUserInput');
    case 'app_restart':
      // The only cause the engine cannot classify itself: the process
      // was gone before it could, so the turn came back as an archived
      // interrupted one on the next assembly (host/recover.go).
      return i18n.t('chat.interruptedAppRestart');
    case undefined:
    case '':
      // The engine's zero cause ("unknown") renders as empty.
      return null;
    default:
      return i18n.t('chat.interrupted');
  }
}

// isUserStop reports whether a non-completed turn ended because the
// user stopped it (the cancel button or a barge-in user message), so
// the turn-end notice can stay concise and provider diagnostics can
// be hidden. The engine reports a deadline and a user stop as the same
// `canceled` status; errorKind is what tells them apart, and a turn
// the deadline ended keeps its diagnostics (and its own words).
export function isUserStop(
  status: TurnStatus | undefined,
  interruptCause?: string,
  errorKind?: string,
): boolean {
  if (status === 'canceled') return errorKind !== 'timeout';
  if (status !== 'interrupted') return false;
  return interruptCause === 'user_cancel' || interruptCause === 'user_input';
}

// friendlyFailure maps the inference error kind to user-safe text: a
// provider failure only needs to tell the user the model call did not go
// through and that retrying is reasonable. It returns null for a failure
// the engine did not classify, so the caller can fall back to the raw
// error rather than claim a generic cause.
export function friendlyFailure(kind?: string): string | null {
  switch (kind) {
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
    case 'timeout':
      return i18n.t('chat.turnTimeout');
    case undefined:
    case '':
      return null;
    default:
      return i18n.t('chat.genericFailure');
  }
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

// mergeTurnDoc appends a produced file, or refreshes its byte count in
// place when the same path is written again.
function mergeTurnDoc(docs: TurnDoc[], path: string, bytes: number): TurnDoc[] {
  const idx = docs.findIndex((d) => d.path === path);
  const entry: TurnDoc = { path, bytes };
  if (idx < 0) return [...docs, entry];
  return [...docs.slice(0, idx), entry, ...docs.slice(idx + 1)];
}

// artifactTurnIndex finds the turn a produced file belongs to. The
// backend attributes the write to the run that made it, so the strip is
// found by run id: while another turn sits above the running one (a
// delegation note the app appended mid-turn, a barge-in replacement),
// the last strip is not the writer.
//
// A run id the store has not recorded yet means the start-turn response
// for that run is still in flight, so the file belongs to the trailing
// live entry — the one with neither a run id nor an archived seq. With
// no writer to point at, the event is dropped rather than merged into
// whatever turn happens to be last.
function artifactTurnIndex(
  list: TurnArtifacts[],
  runID: string | undefined,
): number {
  if (runID) {
    const idx = list.findIndex((t) => t.runID === runID);
    if (idx >= 0) return idx;
  }
  const live = list.length - 1;
  const last = list[live];
  if (!last || last.runID || last.seq !== undefined) return -1;
  return live;
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
      trimItems(msg);
      return next;
    }
    case 'reasoning': {
      const text = part.text ?? '';
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages);
      mergeAppend(msg, 'reasoning', text);
      trimItems(msg);
      return next;
    }
    case 'tool_call': {
      // A tool call ends the preceding text block: fold it so a settled
      // block holds plain text instead of a chunk array.
      const { msg, messages: next } = lastAssistant(messages);
      foldLastTextItem(msg);
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
            seenAt: Date.now(),
          },
        },
      ];
      trimItems(msg);
      return next;
    }
    case 'tool_result': {
      const id = part.result.call_id;
      let next = messages;
      // Walk backwards: a result virtually always belongs to the newest
      // assistant message, so the common case finds it in the first step
      // instead of scanning the whole transcript for every tool result.
      for (let i = messages.length - 1; i >= 0; i--) {
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
              result: capToolResult(
                sanitizeToolResult(toolResultText(part.result.content)),
              ),
              endedAt: Date.now(),
              images: toolResultImages(part.result.content),
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

  // startTurnFor is the shared low-level send: it validates the
  // draft, appends the optimistic user message, and starts one turn
  // in the given conversation. Busy-state policy lives in its callers
  // (send refuses while busy; sendInterrupt barges in; the queue
  // drain fires after a terminal event).
  const startTurnFor = async (
    convID: string,
    text: string,
    attachments: AttachmentView[],
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
    return true;
  };

  // drainQueued sends the single staged draft for a conversation. It
  // is invoked when that conversation's turn reaches its terminal
  // event (Tab queue) or, for interrupt=true, as soon as the awaited
  // run starts.
  const drainQueued = (convID: string) => {
    const queued = get().conversations[convID]?.queued;
    if (!queued) return;
    updateConv(convID, { queued: undefined });
    void startTurnFor(convID, queued.text, queued.attachments);
  };

  // supersededEndState reports whether a superseded run already hit
  // its terminal event while the replacement was still starting. The
  // runConvs entry disappears exactly when turn_end is processed, so
  // its absence (with the status recorded on the artifact) means the
  // old run is no longer alive and must not be "restored".
  const supersededEndState = (
    convID: string,
    runID: string,
  ): { status: TurnStatus; error?: string; errorKind?: string } | undefined => {
    if (get().runConvs[runID] === convID) return undefined;
    const artifact = get().conversations[convID]?.turnArtifacts.find(
      (t) => t.runID === runID,
    );
    const status = artifact?.status
      ? normalizeTurnStatus(artifact.status)
      : undefined;
    if (!artifact || !status) return { status: 'interrupted' };
    return { status, error: artifact.error, errorKind: artifact.errorKind };
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
    attachments: AttachmentView[] = [],
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
    startingActor?.send({ type: 'SEND_STARTED' });
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
      // An Enter pressed while the previous send was still "starting"
      // staged interrupt=true. The run just started, so fire it now;
      // the replacement barges in through the same send path.
      const queued = get().conversations[convID]?.queued;
      if (queued?.interrupt) {
        updateConv(convID, { queued: undefined });
        void startTurnFor(convID, queued.text, queued.attachments);
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
        // The barge-in start failed. If the superseded run is still
        // live, resume watching it (its late streams and terminal
        // event must keep driving the conversation); if its terminal
        // event already passed while this send was starting, absorb
        // that status instead of reporting a fresh failure.
        const superseded = context.supersededRunID;
        const ended = supersededEndState(convID, superseded);
        startingActor?.send({
          type: 'START_FAILED',
          error,
          ...(ended
            ? {
                supersededEndedStatus: ended.status,
                supersededEndedError: ended.error,
                supersededEndedErrorKind: ended.errorKind,
              }
            : {}),
        });
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
          if (!data.run_id) break;
          const actor = stateRoot.registry.get(conversationID);
          if (actor) {
            const snapshot = actor.getSnapshot();
            const value = snapshot.value as unknown as { turn: string };
            const context = snapshot.context as {
              currentRunID?: string;
              lastEndedRunID?: string;
              supersededRunID?: string;
            };
            if (
              context.supersededRunID &&
              data.run_id === context.supersededRunID
            ) {
              // A barge-in is replacing this run; its late deltas must
              // never land after the replacement's user message.
              break;
            }
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
              runConvs,
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
          // beginTurn marks the just-ended run as superseded so the
          // pending terminal event stays inert and the next turn
          // starts cleanly. Failed/cancelled/aborted endings keep the
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
          set((state) => {
            const existing = state.conversations[conversationID];
            const conv = existing ?? emptyConv();
            const runConvs = runID
              ? { ...state.runConvs, [runID]: conversationID }
              : state.runConvs;
            // An event for a run the transcript already holds adds the
            // mapping and nothing else.
            const known =
              runID !== undefined &&
              conv.turnArtifacts.some((t) => t.runID === runID);
            if (!runID || known) {
              if (existing) return { runConvs };
              return {
                runConvs,
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
              runConvs,
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
              paletteOpen: false,
              shortcutsOpen: false,
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
    conversationForRunID: (runID) => get().runConvs[runID],
    pendingInteractConversation: (promptID) =>
      get().pendingPromptConvs[promptID],
    activeWorkspace: () => get().workspace,
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
    const interval = streamFlushInterval(pendingStreamEvents);
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
      session_id: string;
      mode: string;
      think: string;
      model: string;
    },
    request: number,
  ) => {
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
    runConvs: {},
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
        (turnEndData?.run_id ? get().runConvs[turnEndData.run_id] : undefined);
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
          (() => {
            const turn = conversationTurnState(convID);
            return turn.name === 'starting' || turn.name === 'running';
          })() ||
          !state.configured
        ) {
          return;
        }
        // Any fresh manual send supersedes a draft that is still staged
        // (for example after the turn it was queued behind failed).
        updateConv(convID, { queued: undefined });
        await startTurnFor(convID, text, attachments);
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
        const turn = conversationTurnState(convID);
        if (turn.name === 'starting') {
          // No run id exists yet to interrupt; stage the input and fire
          // it as a barge-in the moment the awaited run starts.
          updateConv(convID, {
            queued: { text: trimmed, attachments, interrupt: true },
          });
          return true;
        }
        // Enter is "answer me now": drop anything staged with Tab and
        // start immediately. While a turn is running the engine
        // interrupts it; otherwise this is a normal send.
        updateConv(convID, { queued: undefined });
        await startTurnFor(convID, text, attachments);
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
      const turn = conversationTurnState(convID);
      if (turn.name !== 'running' || !turn.runID) return false;
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
        updateConv(convID, patch);
        if (!waiting) {
          // The run's turn_end settled while this submission was in
          // flight; the conversation owns the text now (either the
          // archive has it or the row is marked undelivered).
          return true;
        }
        const after = conversationTurnState(convID);
        if (
          after.name === 'idle' ||
          after.name === 'succeeded' ||
          after.name === 'failed'
        ) {
          // The run ended between submit and rejection: a normal send is
          // what the user meant, so start one instead of losing the text.
          await startTurnFor(convID, trimmed, []);
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
      const turn = conversationTurnState(convID);
      const busy = turn.name === 'starting' || turn.name === 'running';
      // A live turn takes the text the way a Tab draft would; only drop
      // the row once it is actually staged.
      if (busy && !get().queueInput(row.text, [])) return;
      updateConv(convID, {
        messages: conv.messages.filter((m) => m.id !== id),
      });
      if (!busy) await startTurnFor(convID, row.text, []);
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
      const turn = conversationTurnState(convID);
      if (turn.name !== 'starting' && turn.name !== 'running') {
        return false;
      }
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
                  historySeq: page.historySeq,
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
          const messages = page.messages;
          const turnArtifacts = page.turnArtifacts;
          // A live shell may already hold the current run's streamed
          // messages. Keep them after the archived history; completed
          // shells are replaced by the archive instead of duplicated.
          const keepLive =
            Boolean(existing) &&
            (actorValue?.turn === 'running' || actorValue?.turn === 'starting');
          const mergedMessages = keepLive
            ? [...messages, ...existing.messages]
            : [
                // Undelivered steer rows are not in the archive, so they
                // travel across the rebuild explicitly (see
                // carryUndeliveredSteers).
                ...messages,
                ...carryUndeliveredSteers(existing?.messages, messages),
              ];
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
        stateRoot.registry.markDeleted(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          const viewers = { ...state.viewers };
          delete viewers[id];
          return { conversations, viewers };
        });
        clearPendingIndex(id);
        // The backend only mints when the deleted chat was still the
        // workspace's current conversation once the delete settled.
        // Open that replacement only when this chat is still focused;
        // a selection made while the delete waited wins and should not
        // be stomped by an OPEN_NEW.
        if (next.session_id && activeConversationID() === id) {
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
        void get().loadWorkspaces();
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
