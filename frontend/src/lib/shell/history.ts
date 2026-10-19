import type { StoreApi } from 'zustand';
import {
  HISTORY_PAGE_TURNS,
  INITIAL_HISTORY_TURNS,
  historyPage,
} from '../conv/history';
import { api } from '../api';
import type { ContextUsageDTO } from '../types';
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

// readContextUsage reads the conversation's last measured prompt — the
// numbers the composer's context meter draws — and never rejects: a
// conversation that was never metered and a read that fails both mean
// the same thing to the composer, which is that there is no meter to
// draw. The meter is an annotation on a transcript that loads with or
// without it, so it must not be able to fail a conversation open.
export async function readContextUsage(
  id: string,
): Promise<ContextUsageDTO | undefined> {
  if (!id) return undefined;
  try {
    const usage = await api.sessionContextUsage(id);
    // The zero event is the backend's "never metered": no categories,
    // nothing to draw. Treating it as a value would leave the meter
    // stuck on an empty bar.
    return usage && usage.categories && usage.categories.length > 0
      ? usage
      : undefined;
  } catch {
    return undefined;
  }
}

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
        // The rebuild replaces the conversation wholesale, so the
        // meter's snapshot is read again with it: the numbers belong to
        // the conversation, not to the shell that happened to hold them.
        const [turns, usage] = await Promise.all([
          api.sessionTurns(id, INITIAL_HISTORY_TURNS + 1, 0),
          readContextUsage(id),
        ]);
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
              // A read that found nothing (never metered, or the
              // conversation predates the document) must not erase what
              // this shell already shows for it.
              contextUsage: usage ?? before?.contextUsage,
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
