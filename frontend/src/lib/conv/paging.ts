import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';
import { activeConversationID, createConvCore } from './core';
import { stateRoot } from '../../state/app';
import { api } from '../api';
import {
  TAIL_SYNC_TURNS,
  foldArchivedTurns,
  historyTurnsToState,
  newestArchivedSeq,
} from './history';
import type { MessageView } from './model';
import { carryUnarchivedSteers, settleConversation } from './model';

// Archive reconciliation and the tail sync that follows it: a finished
// turn is replaced by its archived copy, and a tail that could not be
// synced yet is remembered until the archive carries it. S4 moved these
// out of store.ts (move only).

export function createConvPaging(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
) {
  const set = store.setState;
  const get = store.getState;
  const { conversationTurnState, retainLiveConversations } = core;

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
        // The archive is written after the fact, so its rows carry no
        // marker. A turn this window watched stream keeps the one the live
        // rows earned: without it the transcript would fold the tail of a
        // long answer the moment the turn ended, under the reader who was
        // reading it (see StreamItemView).
        const rebuiltMessages = conv.messages
          .slice(start)
          .some((m) => m.items.some((it) => it.kind === 'text' && it.streamed))
          ? rebuilt.messages.map(watchedText)
          : rebuilt.messages;
        // The archive never saw the steers this turn ended without
        // delivering, nor the ones still queued, so the rows carrying
        // that text travel with the rebuild: they are the only copy. The
        // archived copy of the delivered ones comes back tagged by
        // historyToMessages, and the registrations above this turn are
        // untouched by it.
        const carried = carryUnarchivedSteers(
          { ...conv, messages: conv.messages.slice(start) },
          rebuiltMessages,
        ).messages;
        return {
          conversations: {
            ...stateNow.conversations,
            [conversationID]: settleConversation({
              ...conv,
              messages: [
                ...conv.messages.slice(0, start),
                ...rebuiltMessages,
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

  // watchedText marks a rebuilt message's text blocks as text this window
  // saw arrive. Everything else about the archived copy is kept: the
  // marker says what the reader witnessed, not what the row is.
  function watchedText(m: MessageView): MessageView {
    if (!m.items.some((it) => it.kind === 'text')) return m;
    return {
      ...m,
      items: m.items.map((it) =>
        it.kind === 'text' ? { ...it, streamed: true as const } : it,
      ),
    };
  }

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

  // deleteSession drops a conversation, and its pending tail sync goes
  // with it: the anchor it waited for can never arrive.
  const forgetDeferredTailSync = (id: string) => {
    deferredTailSyncs.delete(id);
  };

  return {
    reconcileTurnFromArchive,
    deferredTailSyncs,
    turnIsLive,
    rememberDeferredTailSync,
    syncTranscriptTail,
    drainDeferredTailSync,
    forgetDeferredTailSync,
  };
}
