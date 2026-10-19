import { type ToolImage } from '../toolresult';
import type {
  AttachmentView,
  DelegationNote,
  FileTab,
  InteractDTO,
  StreamPart,
} from '../types';
import { capChars, capTextItem, foldTextItem } from './stream';
import { itemChars } from './text';

// Re-exported so the modules that read them from the conversation model
// keep working; the definitions live in ./text.
export {
  MAX_ITEMS_PER_MESSAGE,
  MAX_REASONING_CHARS,
  MAX_ITEM_TEXT_CHARS,
  MAX_TOOL_RESULT_CHARS,
  MAX_TOOL_ARGS_CHARS,
  itemText,
  itemChars,
  msgSeq,
  newID,
  normalizeArgs,
} from './text';

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

export function viewerDefaults(): FileViewerState {
  return {
    filesOpen: false,
    panelMode: 'files',
    fileTabs: [] as FileTab[],
    fileActive: null as string | null,
    fileTreeDir: '.',
  };
}

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
//
// `text` is what the block shows before its chunks: on a block the cap
// has already trimmed it holds the trim marker, so the marker can never
// be mistaken for content (it is not in the chunk array, and the cap
// cannot drop it as if it were text).
//
// `chars` and `dropped` are the running totals the streaming paths keep
// so that neither the append nor the cap has to join the chunks: `chars`
// is the block's display length (text + chunks), `dropped` is how much
// the cap has already cut. Both are optional — a block that came from
// history, or one that has been folded, holds neither.
export interface TextItemFields {
  id: string;
  text: string;
  chunks?: string[];
  chars?: number;
  dropped?: number;
  // streamed marks a block whose text arrived through the live stream in
  // this window, so the transcript knows the reader watched it grow and
  // keeps it whole instead of folding its tail behind a control (see
  // StreamItemView). Blocks replayed from history carry no marker: they
  // were already written when the window saw them.
  streamed?: true;
}

// AssistantItem preserves stream order; the two text kinds stay separate
// union members so `Extract<AssistantItem, { kind: 'text' }>` — the
// pattern the renderers use to pick text blocks — keeps working.
export type AssistantItem =
  | ({ kind: 'reasoning' } & TextItemFields)
  | ({ kind: 'text' } & TextItemFields)
  | { kind: 'tool_call'; id: string; tool: ToolView };

// TextItem is either text kind, for helpers that treat them alike.
export type TextItem = ({ kind: 'reasoning' } | { kind: 'text' }) &
  TextItemFields;

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

// carryUnarchivedSteers picks the interjection rows the archive never
// saw and appends them to a transcript rebuilt from it. Two shapes are
// not in the archive, because nothing appended their text to the
// conversation: the rows a turn ended without delivering
// ('undelivered') and the rows still queued for a boundary ('pending').
// Carrying them over keeps the only copy of the words; dropping them
// drops the text.
//
// A pending row travels only while a registration still names it: that
// entry is what its run's turn_end classifies the row by, and a rebuild
// that kept the row but dropped the entry would leave it claiming a
// boundary that already came. The registrations that do travel come
// with it for the same reason.
//
// A row whose text the rebuilt transcript already holds as a delivered
// interjection is dropped instead: that shape comes from a boundary
// that did take the message while the run's terminal event was lost, so
// the settle-on-drop below guessed wrong and the archive is the truth.
export function carryUnarchivedSteers(
  conv: ConversationState | undefined,
  rebuilt: MessageView[],
): { messages: MessageView[]; steerSent: QueuedSteer[] } {
  const registered = new Set((conv?.steerSent ?? []).map((s) => s.messageID));
  const delivered = new Set(
    rebuilt.filter((m) => m.steer === 'delivered').map((m) => m.text),
  );
  const messages = (conv?.messages ?? []).filter((m) => {
    if (delivered.has(m.text)) return false;
    if (m.steer === 'undelivered') return true;
    return m.steer === 'pending' && registered.has(m.id);
  });
  const kept = new Set(messages.map((m) => m.id));
  return {
    messages,
    steerSent: (conv?.steerSent ?? []).filter((s) => kept.has(s.messageID)),
  };
}

// capUndeliveredSteers bounds the number of undelivered steer rows one
// conversation keeps. Each one is the only copy of its text; a
// conversation that keeps producing them should still not grow without
// bound, so the oldest are dropped (the same policy the old card list
// used).
export function capUndeliveredSteers(messages: MessageView[]): MessageView[] {
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
export function settleSteerDelivery(
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
  // whole session. The transcript cap moves both with its cut: the cursor
  // then names what is still loaded, or one past the turn the cut took
  // last (see capConversation). historyLoading guards concurrent page
  // requests.
  historySeq?: number;
  historyHasMore?: boolean;
  historyLoading?: boolean;
  // steerSent tracks steer rows still awaiting their run's turn_end;
  // the rows themselves carry the state the end stamps on them
  // (MessageView.steer). A row leaves the list once its state is
  // settled, so the list holds exactly the rows a still-running turn was
  // handed, and it is render-process state that starts empty again after
  // a reload. Rows the archive cannot see (undelivered and still-queued
  // ones) are carried across transcript rebuilds — see
  // carryUnarchivedSteers.
  steerSent?: QueuedSteer[];
  // footprint is the last measurement settleConversation took (see
  // there). It caches the two byte totals, not the transcript: a
  // conversation that loses it only pays one full walk on its next
  // write, and one whose rows are replaced immutably keeps it exact.
  footprint?: TranscriptFootprint;
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

export let turnSeq = 0;

export const newTurnID = () => `live-${++turnSeq}`;

// MAX_CONV_MESSAGES bounds the in-memory transcript per conversation.
// Older messages stay in the backend archive (sessionTurns) and can be
// re-opened via resume; keeping them in the store would grow memory
// without bound on long-running sessions.
export const MAX_CONV_MESSAGES = 800;

// MAX_UNDELIVERED_STEERS bounds the per-conversation list of steered
// messages a turn ended without delivering. Each one is a card in the
// transcript; a conversation that keeps producing them should not grow
// the list without bound.
export const MAX_UNDELIVERED_STEERS = 20;

// MAX_CONV_MEDIA_BYTES bounds the inline image payload — base64 data
// URLs, counted in string characters — one conversation keeps in the
// store. Only a single image is capped anywhere else
// (imageutil.MaxInlineImageBytes, 10 MiB): the archive's screenshot-heavy
// conversation holds 84 inline frames (16 MiB) on its own, and a renderer
// that pages back through a few turns of them is where hundreds of
// megabytes of WebKit Malloc came from. An evicted entry keeps its path,
// which is all either viewer needs: AttachmentImage re-reads its preview
// on mount and the view_image card re-reads the file on expand.
export const MAX_CONV_MEDIA_BYTES = 16 << 20;

// MAX_CONV_TEXT_BYTES bounds the text one conversation keeps: message
// text, block text, tool arguments and tool results. It is the safety net
// under the per-block caps (MAX_ITEM_TEXT_CHARS, MAX_TOOL_ARGS_CHARS,
// MAX_TOOL_RESULT_CHARS): those bound one block, and a long turn holds up
// to MAX_ITEMS_PER_MESSAGE of them.
export const MAX_CONV_TEXT_BYTES = 16 << 20;

// BUDGET_KEEP_MESSAGES is the tail both budgets leave alone. The
// transcript renders the newest turns, so trimming there would be
// visible work on text the reader is looking at; everything older is off
// screen or a scroll away, and getting it back is one fetch.
export const BUDGET_KEEP_MESSAGES = 24;

// BUDGET_FOLD_CHARS is the tail one block keeps once the text budget
// folds it: enough for the block to still read as itself.
export const BUDGET_FOLD_CHARS = 2 << 10;

// capConversation trims the oldest messages past the in-memory cap and
// re-bases per-turn artifact strip indexes onto the trimmed array. The
// cut lands on a turn boundary, so no row ever stays behind without the
// strip that owns it: a half turn would lose its files and its fold
// header, and the live turn — the row a running run, its deltas and its
// archived copy are looked up by — would lose that strip while keeping
// its rows. The history cursor moves with the cut, so the turns a trim
// drops are one "load earlier" page away rather than gone.
export function capConversation(conv: ConversationState): ConversationState {
  if (conv.messages.length <= MAX_CONV_MESSAGES) return conv;
  const length = conv.messages.length;
  const above = length - MAX_CONV_MESSAGES;
  // back is the largest turn boundary at or below the exact cut, and
  // next is the first boundary past it; boundaries are the strips'
  // starts, in ascending order. A strip that starts at the end of the
  // transcript owns no rows — the seam a run met on the wire opens
  // before its first row lands — so it is not a boundary a cut may land
  // on: that cut would drop every row in the store.
  let back = 0;
  let next = 0;
  for (const turn of conv.turnArtifacts) {
    if (turn.start >= length) break;
    if (turn.start > above) {
      next = turn.start;
      break;
    }
    if (turn.start > back) back = turn.start;
  }
  // Cut at `back`: the trim backs up to that boundary, overshooting the
  // cap by at most one turn. When backing up would keep the whole
  // transcript (the oldest turn reaches past the cut), the cut moves
  // down to `next` instead — under the cap by up to one turn, and every
  // kept row still begins at the boundary of its own turn.
  let drop = back;
  if (drop === 0) {
    if (next > 0) {
      drop = next;
    } else if (conv.turnArtifacts.length === 0) {
      // No turn structure to respect: the exact cut is the only bound.
      drop = above;
    } else {
      // One turn that owns rows — the live one, normally. Its rows are
      // what the reader is watching, and cutting inside it would take
      // the strip while keeping the rows; the cap yields to it.
      return conv;
    }
  }
  const messages = conv.messages.slice(drop);
  // The cursor moves with the cut: it names the oldest archived turn
  // still loaded, or — when the cut took every one — one past the newest
  // it took, so "load earlier" starts on the dropped turns themselves
  // instead of below them. Turns the cut dropped are turns the reader
  // can page back in, whether the window that loaded the transcript
  // thought older history existed or not.
  let keptSeq = 0;
  let droppedSeq = 0;
  const turnArtifacts: TurnArtifacts[] = [];
  for (const turn of conv.turnArtifacts) {
    const start = turn.start - drop;
    if (start < 0) {
      if (turn.seq !== undefined && turn.seq > droppedSeq)
        droppedSeq = turn.seq;
      continue;
    }
    if (turn.seq !== undefined && (keptSeq === 0 || turn.seq < keptSeq)) {
      keptSeq = turn.seq;
    }
    turnArtifacts.push({ ...turn, start });
  }
  const historySeq =
    keptSeq || (droppedSeq > 0 ? droppedSeq + 1 : conv.historySeq);
  const historyHasMore = conv.historyHasMore || droppedSeq > 0;
  return { ...conv, messages, turnArtifacts, historySeq, historyHasMore };
}

// The three helpers below read a message defensively (`?? []`): they run
// on every stream flush, so one row that arrived without an array — an
// old store snapshot, a test fixture, a fixture-shaped archive row —
// must not be able to throw inside the store's write path.

// mediaCharsOf sums the inline image bytes one message holds: attachment
// previews above a user bubble and the image parts of a view_image
// result. Everything else about an attachment is a path and a few
// numbers.
export function mediaCharsOf(msg: MessageView): number {
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
export function textCharsOf(msg: MessageView): number {
  let total = msg.text?.length ?? 0;
  for (const item of msg.items ?? []) {
    if (item.kind === 'tool_call') {
      total += (item.tool.args?.length ?? 0) + (item.tool.result?.length ?? 0);
      continue;
    }
    total += itemChars(item);
  }
  return total;
}

// dropMessageMedia clears the inline images of one message, keeping the
// paths. Returns the message unchanged when it held none.
export function dropMessageMedia(msg: MessageView): MessageView {
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
export function foldMessageText(msg: MessageView): MessageView {
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

// TranscriptFootprint is what one conversation's transcript costs: the
// two totals the byte budgets are expressed in, plus the array they were
// measured over.
export interface TranscriptFootprint {
  messages: MessageView[];
  media: number;
  text: number;
}

export function measureTranscript(
  messages: MessageView[],
): TranscriptFootprint {
  let media = 0;
  let text = 0;
  for (const msg of messages) {
    media += mediaCharsOf(msg);
    text += textCharsOf(msg);
  }
  return { messages, media, text };
}

// remeasure carries a footprint over to a new transcript: the head and
// the tail the write left alone are found by identity and the rows
// between them are what gets measured. A write that replaced every row,
// or reordered them, just measures all of it again.
export function remeasure(
  footprint: TranscriptFootprint,
  messages: MessageView[],
): TranscriptFootprint {
  const old = footprint.messages;
  const shared = Math.min(old.length, messages.length);
  let head = 0;
  while (head < shared && old[head] === messages[head]) head += 1;
  let oldEnd = old.length;
  let newEnd = messages.length;
  while (
    oldEnd > head &&
    newEnd > head &&
    old[oldEnd - 1] === messages[newEnd - 1]
  ) {
    oldEnd -= 1;
    newEnd -= 1;
  }
  let media = footprint.media;
  let text = footprint.text;
  for (let i = head; i < oldEnd; i += 1) {
    media -= mediaCharsOf(old[i]);
    text -= textCharsOf(old[i]);
  }
  for (let i = head; i < newEnd; i += 1) {
    media += mediaCharsOf(messages[i]);
    text += textCharsOf(messages[i]);
  }
  return { messages, media, text };
}

// settleConversation is the one place a conversation's in-memory size is
// enforced: the message cap first, then the two byte budgets. It is
// called from every write path that can grow a transcript (a stream
// flush, a send, a history page, an archive reconcile), so no path can
// quietly bypass a budget.
//
// It is called on every write, including the ones that only touch a
// title or a mode, so it does not walk the transcript: the last
// measurement is kept on the conversation (`footprint`) and the next one
// runs over the rows that changed, which a write finds by identity —
// writes replace the messages array and the rows they do not touch keep
// their objects (lastAssistant copies the row it grows). A measurement
// that cannot be expressed as such a difference is the whole transcript
// again, which is what happens on the first settle after a load.
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
export function settleConversation(
  conv: ConversationState,
  opts?: { cap?: boolean },
): ConversationState {
  const capped = opts?.cap === false ? conv : capConversation(conv);
  // The footprint the last settle recorded, brought up to date. A
  // recorded array that is still the current one means no row moved at
  // all; otherwise the difference between the two is measured.
  const recorded = capped.footprint;
  const totals =
    !recorded || recorded.messages === capped.messages
      ? (recorded ?? measureTranscript(capped.messages))
      : remeasure(recorded, capped.messages);
  const measured =
    totals === recorded ? capped : { ...capped, footprint: totals };
  let media = totals.media;
  let text = totals.text;
  const overMedia = media > MAX_CONV_MEDIA_BYTES;
  const overText = text > MAX_CONV_TEXT_BYTES;
  if (!overMedia && !overText) return measured;
  // The oldest messages give up their bytes first: dropping a data URL
  // costs a re-fetch when the row is looked at again, folding text costs
  // reading a marker. Media goes first because it is what a screenshot-
  // heavy transcript spends its memory on.
  const keepFrom = Math.max(0, measured.messages.length - BUDGET_KEEP_MESSAGES);
  let changed = false;
  const messages = measured.messages.map((msg, index) => {
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
  if (!changed) return measured;
  return { ...measured, messages, footprint: measureTranscript(messages) };
}

export const emptyConv = (
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

export const errorMessage = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

// attachmentPart lowers one staged attachment into the message wire
// form: images/audio/video become URL-sourced media parts (the backend
// persists them and the prepare hook inlines the bytes), anything else
// becomes a file part.
export function attachmentPart(a: AttachmentView): StreamPart {
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

export const baseName = (p: string) => p.split(/[\\/]/).pop() ?? p;

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
