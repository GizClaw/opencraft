import type { StoreApi } from 'zustand';
import {
  HISTORY_PAGE_TURNS,
  INITIAL_HISTORY_TURNS,
  historyPage,
} from '../conv/history';
import { api } from '../api';
import {
  carryUnarchivedSteers,
  emptyConv,
  errorMessage,
  settleConversation,
} from '../conv/model';
import { createConvCore } from '../conv/core';
import { stateRoot } from '../../state/app';
import type { StoreState } from '../store';

// The transcript's own history actions: load an earlier page, and try
// the tail again after it failed. S4 moved these out of store.ts
// (move only).

export function createShellHistory(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
): Pick<StoreState, 'loadEarlierHistory' | 'retryTranscript'> {
  const set = store.setState;
  const get = store.getState;
  const { updateConv } = core;

  return {
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
                  // An empty page carries seq 0: keeping it would say
                  // "nothing below here is loaded" about a transcript
                  // that is, and the next incremental read would start
                  // from the beginning of the archive.
                  historySeq: added > 0 ? page.historySeq : current.historySeq,
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
        // The steers the archive never saw travel across the rebuild with
        // the registrations that still owe them a boundary: the rows are
        // the only copy of their text.
        const carried = carryUnarchivedSteers(before, page.messages);
        set((state) => ({
          conversations: {
            ...state.conversations,
            [id]: settleConversation({
              ...emptyConv(),
              mode: state.conversations[id]?.mode ?? 'workspace',
              think: state.conversations[id]?.think ?? 'medium',
              model: state.conversations[id]?.model ?? '',
              messages: [...page.messages, ...carried.messages],
              ...(carried.steerSent.length > 0
                ? { steerSent: carried.steerSent }
                : {}),
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
  };
}
