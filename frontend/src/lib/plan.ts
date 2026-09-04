import type { MessageView } from './store';

export interface PlanStepView {
  step?: string;
  status?: string;
}

export interface PlanSnapshot {
  explanation?: string;
  items: PlanStepView[];
}

export interface PlanPanelState {
  plan: PlanSnapshot;
  live: boolean;
}

const isPlanCall = (
  item: MessageView['items'][number],
): item is Extract<MessageView['items'][number], { kind: 'tool_call' }> =>
  item.kind === 'tool_call' && item.tool.name === 'update_plan';

// planNeedsRefresh reports whether a messages update can change the
// cached plan snapshot. Plain text deltas keep every update_plan item
// reference intact, so callers can skip the full latestPlan scan until
// a new plan call arrives, a tool result settles one, or the message
// layout changes (retry/history).
export function planNeedsRefresh(
  prev: MessageView[],
  next: MessageView[],
): boolean {
  if (prev.length !== next.length) return true;
  const prevById = new Map(prev.map((m) => [m.id, m]));
  const nextIds = new Set(next.map((m) => m.id));
  for (const msg of next) {
    const old = prevById.get(msg.id);
    if (old === msg) continue;
    const prevPlan = old?.items.find(isPlanCall);
    const nextPlan = msg.items.find(isPlanCall);
    if (prevPlan !== nextPlan) return true;
  }
  for (const old of prev) {
    if (!nextIds.has(old.id) && old.items.some(isPlanCall)) return true;
  }
  return false;
}

// latestPlan scans the transcript for update_plan calls and returns
// the newest non-empty snapshot, so the top-left panel tracks the
// current plan across live turns and resumed sessions alike. When the
// newest call's args are not ready yet (e.g. the stream just started),
// it falls back to the previous valid snapshot while still reflecting
// the running state, so the panel never drops the live indicator.
export function latestPlan(messages: MessageView[]): PlanPanelState | null {
  // last holds the newest update_plan call's run state; lastValid holds
  // the most recent parseable non-empty snapshot.
  let last: { live: boolean } | null = null;
  let lastValid: PlanSnapshot | null = null;
  for (const msg of messages) {
    for (const item of msg.items) {
      if (item.kind !== 'tool_call' || item.tool.name !== 'update_plan') {
        continue;
      }
      last = { live: item.tool.status === 'running' };
      let args: Record<string, unknown> | null = null;
      try {
        const v: unknown = JSON.parse(item.tool.args);
        if (v && typeof v === 'object') args = v as Record<string, unknown>;
      } catch {
        args = null;
      }
      if (args && Array.isArray(args.plan) && args.plan.length > 0) {
        lastValid = {
          explanation:
            typeof args.explanation === 'string' ? args.explanation : undefined,
          items: args.plan as PlanStepView[],
        };
      }
    }
  }
  if (!last) return null;
  // A valid newest call is shown directly; an invalid (empty-args) one
  // falls back to the previous valid snapshot, or an empty placeholder
  // if there is none, keeping the live run state either way.
  return {
    plan: lastValid ?? {
      items: [],
    },
    live: last.live,
  };
}
