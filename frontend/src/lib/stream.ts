import type { AssistantItem, MessageView } from './store';
import type { StreamDelta, UIEvent } from './types';

export type ToolCallItem = Extract<AssistantItem, { kind: 'tool_call' }>;

// update_plan renders once in the top-left plan panel instead of as
// transcript/stream cards, so its calls are dropped everywhere else.
export function isPlanCall(item: AssistantItem): boolean {
  return item.kind === 'tool_call' && item.tool.name === 'update_plan';
}

// Tools rendered as always-visible full blocks are never folded into a
// consecutive-run group: their content matters in place (patch diffs,
// written file contents), not hidden behind a "Ran N tools" summary.
export const nonGroupedTools = new Set(['apply_patch', 'write_file']);

// groupToolCalls merges consecutive tool calls into groups so a burst
// of tool executions renders as one collapsible block instead of a
// stack of cards. Non-tool items are passed through unchanged, except
// hidden reasoning traces, which no longer split a visible tool burst
// in the chat transcript.
export function groupToolCalls(
  items: AssistantItem[],
): (AssistantItem | ToolCallItem[])[] {
  const out: (AssistantItem | ToolCallItem[])[] = [];
  let cur: ToolCallItem[] | null = null;
  for (const item of items) {
    if (isPlanCall(item) || item.kind === 'reasoning') {
      continue;
    }
    if (item.kind === 'tool_call' && !nonGroupedTools.has(item.tool.name)) {
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
  return out;
}

// visibleStreamItems flattens a subagent stream into its renderable
// items in arrival order, dropping plan calls (they render once in the
// plan panel). Shared by the subagent sidebar and any other consumer
// so item visibility never drifts between render paths.
export function visibleStreamItems(messages: MessageView[]): AssistantItem[] {
  return messages.flatMap((m) => m.items).filter((it) => !isPlanCall(it));
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
