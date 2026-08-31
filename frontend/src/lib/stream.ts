import type { AssistantItem, MessageView } from './store';

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
// stack of cards. Non-tool items are passed through unchanged.
export function groupToolCalls(
  items: AssistantItem[],
): (AssistantItem | ToolCallItem[])[] {
  const out: (AssistantItem | ToolCallItem[])[] = [];
  let cur: ToolCallItem[] | null = null;
  for (const item of items) {
    if (isPlanCall(item)) {
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
