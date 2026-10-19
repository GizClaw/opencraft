import { sanitizeToolResult } from '../ansi';
import { toolResultImages, toolResultText } from '../toolresult';
import type { StreamDelta } from '../types';
import type { MessageView, TextItem } from './model';
import {
  MAX_ITEMS_PER_MESSAGE,
  MAX_ITEM_TEXT_CHARS,
  MAX_REASONING_CHARS,
  MAX_TOOL_RESULT_CHARS,
  itemChars,
  itemText,
  newID,
  normalizeArgs,
} from './text';

export function mergeAppend(
  msg: MessageView,
  kind: 'text' | 'reasoning',
  text: string,
) {
  const items = msg.items;
  const lastItem = items[items.length - 1];
  if (lastItem && lastItem.kind === kind) {
    // Append to the block's chunks instead of concatenating the whole
    // answer: the join happens once per render, where the string is
    // needed anyway, and the cap below can drop the oldest chunks. The
    // running length is bumped by the delta, so the cap never measures
    // the block either.
    const chunks = lastItem.chunks ?? [lastItem.text];
    chunks.push(text);
    msg.items = [
      ...items.slice(0, -1),
      capTextItem({
        ...lastItem,
        chunks,
        // A block that had no chunks held its content in `text`; now
        // that content is chunks[0]. A block that had chunks keeps its
        // `text`, which is where the trim marker lives.
        text: lastItem.chunks ? lastItem.text : '',
        chars: itemChars(lastItem) + text.length,
      }),
    ];
  } else {
    msg.items = [
      ...items,
      capTextItem({
        kind,
        id: newID('part'),
        text,
        chars: text.length,
        // Only the live path marks a block this way: a delta is proof the
        // reader (or at least this window) saw the text arrive, which is
        // what keeps the transcript from folding it away at the turn end.
        streamed: true,
      }),
    ];
  }
}

// capTextItem enforces the per-block text bound. Reasoning keeps its
// tail (it is never rendered); visible text keeps head and tail with a
// marker in between. The chunk array is trimmed in place, so a block that
// already dropped its oldest chunks is not re-joined on every delta, and
// the marker counts only content: it is carried in `text` rather than in
// the chunk array, so a later fold can neither drop it as text nor add
// its length to what the marker reports.
// `foldTo` overrides the bound: the conversation text budget folds an old
// message's blocks down to a tail instead of dropping them outright.
export function capTextItem<T extends TextItem>(item: T, foldTo?: number): T {
  const limit =
    foldTo ??
    (item.kind === 'reasoning' ? MAX_REASONING_CHARS : MAX_ITEM_TEXT_CHARS);
  if (itemChars(item) <= limit) {
    return item;
  }
  if (item.chunks && item.chunks.length > 0) {
    // The chunks are the block's content; `text` (when it is set) is the
    // marker of an earlier trim, and `head` is what it adds.
    const chunks = item.chunks;
    let size = 0;
    for (const chunk of chunks) size += chunk.length;
    let dropped = item.dropped ?? 0;
    const head = item.text.length;
    while (chunks.length > 1 && head + size > limit) {
      const cut = chunks.shift();
      if (cut === undefined) break;
      dropped += cut.length;
      size -= cut.length;
    }
    // One chunk left and still over: what it cannot keep goes too, and
    // counts the same way.
    const overflow = head + size - limit;
    if (overflow > 0) {
      dropped += overflow;
      chunks[0] = chunks[0].slice(overflow);
      size -= overflow;
    }
    const text = `…[trimmed ${dropped} chars]`;
    return { ...item, chunks, text, dropped, chars: text.length + size };
  }
  const total = item.text;
  if (item.kind === 'reasoning') {
    const tail = total.slice(-limit);
    const text = `…[trimmed ${total.length - tail.length} chars]` + tail;
    return {
      ...item,
      text,
      chars: text.length,
    };
  }
  const head = Math.floor(limit / 2);
  const tail = limit - head;
  const text = `${total.slice(0, head)}\n…[trimmed ${total.length - limit} chars]…\n${total.slice(-tail)}`;
  return {
    ...item,
    text,
    chars: text.length,
  };
}

// capChars keeps the head and the tail of one long string with a marker
// between them — the shape capTextItem gives a text block, and the same
// one both the per-result cap and the conversation budget use. Neither
// end is arbitrary: a dump says what it is at the top and why it failed
// at the bottom.
export function capChars(text: string, limit: number): string {
  if (text.length <= limit) return text;
  const head = Math.floor(limit / 2);
  const tail = limit - head;
  return `${text.slice(0, head)}\n…[trimmed ${text.length - limit} chars]…\n${text.slice(-tail)}`;
}

// capToolResult enforces MAX_TOOL_RESULT_CHARS on one result string.
export function capToolResult(text: string): string {
  return capChars(text, MAX_TOOL_RESULT_CHARS);
}

// foldTextItem turns a streaming block's chunks back into one string.
// Called when the block ends (a tool call arrives, the message ends), so
// a settled message holds plain text and no chunk array.
export function foldTextItem<T extends TextItem>(item: T): T {
  if (!item.chunks) return item;
  const text = itemText(item);
  const folded = { ...item, text, chars: text.length, dropped: undefined };
  delete (folded as TextItem).chunks;
  return folded;
}

// foldLastTextItem folds the trailing text block of a message, if any.
export function foldLastTextItem(msg: MessageView) {
  const last = msg.items[msg.items.length - 1];
  if (!last || last.kind === 'tool_call' || !last.chunks) return;
  msg.items = [...msg.items.slice(0, -1), foldTextItem(last)];
}

// trimItems enforces MAX_ITEMS_PER_MESSAGE on one message, folding the
// overflow into droppedItems.
export function trimItems(msg: MessageView) {
  if (msg.items.length <= MAX_ITEMS_PER_MESSAGE) return;
  const drop = msg.items.length - MAX_ITEMS_PER_MESSAGE;
  msg.items = msg.items.slice(drop);
  msg.droppedItems = (msg.droppedItems ?? 0) + drop;
}

// lastAssistant returns a mutable copy of the last assistant message
// (creating one when needed) plus a NEW messages array, so every
// stream delta produces fresh references and React re-renders.
//
// runStart is the index the run's own rows begin at (its strip's start):
// a trailing assistant row above it belongs to another turn — a finished
// one, or another run's — so the delta opens a row of its own instead of
// growing that turn's answer.
export function lastAssistant(
  messages: MessageView[],
  runStart: number,
): {
  msg: MessageView;
  messages: MessageView[];
} {
  const lastIndex = messages.length - 1;
  const last = messages[lastIndex];
  if (lastIndex < runStart || !last || last.role !== 'assistant') {
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

// applyStream folds one stream delta into a message list and returns
// the new list (immutable). runStart bounds the fold: deltas only touch
// rows the run owns (see lastAssistant).
export function applyStream(
  messages: MessageView[],
  delta: StreamDelta,
  runStart: number,
): MessageView[] {
  if (delta.type !== 'part' || !delta.part) return messages;
  const part = delta.part;
  switch (part.type) {
    case 'text': {
      const text = part.text ?? '';
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages, runStart);
      mergeAppend(msg, 'text', text);
      trimItems(msg);
      return next;
    }
    case 'reasoning': {
      const text = part.text ?? '';
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages, runStart);
      mergeAppend(msg, 'reasoning', text);
      trimItems(msg);
      return next;
    }
    case 'tool_call': {
      // A tool call ends the preceding text block: fold it so a settled
      // block holds plain text instead of a chunk array.
      const { msg, messages: next } = lastAssistant(messages, runStart);
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
      // The walk stops at the run's own rows: a call below the seam was
      // not this run's (the id would have to be reused across turns),
      // and stamping it would be a delta landing outside its turn.
      for (let i = messages.length - 1; i >= runStart; i--) {
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
