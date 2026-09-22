import type { AssistantItem } from './store';
import type { StreamDelta, UIEvent } from './types';

export type ToolCallItem = Extract<AssistantItem, { kind: 'tool_call' }>;

// The two stream cadences. A queue of deltas is committed at most once
// per interval, so one number decides how live streaming feels and how
// much work a token burst costs.
//
// Visible text keeps 100ms: flushing every animation frame redraws the
// transcript ~60 times a second for prose nobody reads that fast, and
// ten updates a second still looks like a stream.
//
// A queue that holds nothing but reasoning is a different case and gets
// its own, calmer beat. Nothing in the transcript renders reasoning —
// the activity card's thought block is the only reader, and it shows a
// tail meant to be skimmed as a summary rather than read token by token.
// Those commits are not free either: the block is the largest text the
// card lays out, and every commit re-pins its scroller. Four updates a
// second is still more than anyone reads; 250ms keeps a long thinking
// phase from doing the work of a visible answer.
export const STREAM_TEXT_FLUSH_INTERVAL_MS = 100;
export const STREAM_REASONING_FLUSH_INTERVAL_MS = 250;

// streamFlushInterval picks the cadence for the deltas waiting to be
// committed. Mixed queues (a thought and then prose, or a tool call
// arriving mid-reasoning) take the text cadence: the answer's own words
// must never be the slow part.
export function streamFlushInterval(events: UIEvent[]): number {
  if (events.length === 0) return STREAM_TEXT_FLUSH_INTERVAL_MS;
  const reasoningOnly = events.every((ev) => {
    const delta = (ev.data as { delta?: StreamDelta }).delta;
    return delta?.type === 'part' && delta.part?.type === 'reasoning';
  });
  return reasoningOnly
    ? STREAM_REASONING_FLUSH_INTERVAL_MS
    : STREAM_TEXT_FLUSH_INTERVAL_MS;
}

// update_plan renders once in the top-left plan panel instead of as
// transcript/stream cards, so its calls are dropped everywhere else.
export function isPlanCall(item: AssistantItem): boolean {
  return item.kind === 'tool_call' && item.tool.name === 'update_plan';
}

// groupCache memoizes the grouping per items array. Message items are
// replaced immutably (applyStream swaps the array instead of mutating
// it), so the array identity is a sound cache key: a message that did
// not change keeps its groups across renders, which is what lets the
// transcript rows — and the expanded process rows in particular — bail
// out of the per-flush re-render instead of walking every item again.
// Callers must treat the result (and the group arrays in it) as
// read-only.
const groupCache = new WeakMap<
  AssistantItem[],
  (AssistantItem | ToolCallItem[])[]
>();

// groupToolCalls merges consecutive tool calls into groups so a burst
// of tool executions renders as one collapsible block instead of a
// stack of cards. Every tool takes part, patch and write calls
// included: their bodies are the biggest thing a long turn can mount,
// so they belong behind the same fold rather than inline in the
// transcript. Non-tool items are passed through unchanged, except
// hidden reasoning traces, which no longer split a visible tool burst
// in the chat transcript.
export function groupToolCalls(
  items: AssistantItem[],
): (AssistantItem | ToolCallItem[])[] {
  const cached = groupCache.get(items);
  if (cached) return cached;
  const out: (AssistantItem | ToolCallItem[])[] = [];
  let cur: ToolCallItem[] | null = null;
  for (const item of items) {
    if (isPlanCall(item) || item.kind === 'reasoning') {
      continue;
    }
    if (item.kind === 'tool_call') {
      if (!cur) cur = [];
      cur.push(item);
    } else {
      if (cur) {
        out.push(cur);
        cur = null;
      }
      out.push(item);
    }
  }
  if (cur) out.push(cur);
  groupCache.set(items, out);
  return out;
}

// coalesceStreamEvents merges contiguous text/reasoning deltas of the
// same stream into one wire event, so a high-token burst becomes one
// immutable transcript fold per phase per flush instead of one fold
// per token. Events from different runs/conversations are flushed in
// their original arrival order instead of being reordered per group.
export function coalesceStreamEvents(events: UIEvent[]): UIEvent[] {
  const out: UIEvent[] = [];
  const emitAccumulated = (
    ev: UIEvent,
    kind: 'text' | 'reasoning',
    text: string,
  ) => {
    if (!text) return;
    const data = ev.data as {
      delta: StreamDelta;
    };
    out.push({
      ...ev,
      data: {
        ...data,
        delta: {
          ...data.delta,
          type: 'part',
          part: { type: kind, text },
        },
      },
    });
  };
  let activeKey = '';
  let activeKind: 'text' | 'reasoning' | undefined;
  let activeText = '';
  let activeTemplate: UIEvent | undefined;
  const flushActive = () => {
    if (activeTemplate && activeKind && activeText) {
      emitAccumulated(activeTemplate, activeKind, activeText);
    }
    activeKey = '';
    activeKind = undefined;
    activeText = '';
    activeTemplate = undefined;
  };
  for (const ev of events) {
    const data = ev.data as {
      conversation_id?: string;
      run_id?: string;
      delta?: StreamDelta;
    };
    const key = `${data.conversation_id ?? ''}\u0000${data.run_id ?? ''}`;
    const delta = data.delta;
    const part = delta?.part;
    const isText = delta?.type === 'part' && part?.type === 'text';
    const isReasoning = delta?.type === 'part' && part?.type === 'reasoning';
    if (!isText && !isReasoning) {
      flushActive();
      out.push(ev);
      continue;
    }
    const kind: 'text' | 'reasoning' = isText ? 'text' : 'reasoning';
    const text = (part as { text?: string }).text ?? '';
    if (!text) {
      // Empty deltas are ordering boundaries only for the renderer;
      // keep them in place rather than folding them into a neighbor.
      flushActive();
      out.push(ev);
      continue;
    }
    if (key !== activeKey || kind !== activeKind) {
      flushActive();
      activeKey = key;
      activeKind = kind;
      activeTemplate = ev;
    }
    activeText += text;
  }
  flushActive();
  return out;
}
