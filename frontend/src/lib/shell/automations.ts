import type { StoreApi } from 'zustand';
import { api } from '../api';
import type { StoreState } from '../store';

// The automation list and one task's recent runs: both are refreshed
// by several callers, so each keeps its own newest-answer-wins counter.
// S4 moved these out of store.ts (move only).

export function createShellAutomations(
  store: StoreApi<StoreState>,
): Pick<StoreState, 'loadAutomations' | 'loadAutomationRuns'> {
  const set = store.setState;

  // The automation list and one task's run list are refreshed by the
  // same three callers; only the newest answer of each may apply.
  let automationsLoadSeq = 0;

  const automationRunsLoadSeq = new Map<string, number>();

  return {
    loadAutomations: async () => {
      // Starting a task, its settlement and an explicit refresh can
      // overlap; the answer that arrives last is the one the list shows,
      // like loadWorkspaces (a stale run's list would reinstate the
      // Cancel button of a task that just started).
      const seq = (automationsLoadSeq += 1);
      try {
        const next = (await api.automations()) ?? [];
        if (seq !== automationsLoadSeq) return;
        set({ automations: next });
      } catch {
        // best-effort
      }
    },

    loadAutomationRuns: async (taskId: string) => {
      const seq = (automationRunsLoadSeq.get(taskId) ?? 0) + 1;
      automationRunsLoadSeq.set(taskId, seq);
      try {
        const list = (await api.automationRuns(taskId)) ?? [];
        if (automationRunsLoadSeq.get(taskId) !== seq) return;
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
  };
}
