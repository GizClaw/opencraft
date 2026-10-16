import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';
import type { UIEvent } from '../types';
import {
  ConversationState,
  FileViewerState,
  emptyConv,
  settleConversation,
  viewerDefaults,
} from './model';
import {
  SubmitKind,
  submissionEffect,
} from '../../state/machines/conversation';
import {
  coalesceStreamEvents,
  isReasoningDelta,
  recordFlushDuration,
  streamFlushInterval,
} from '../stream';
import { routeBackendEvent, type EventDataSink } from '../../state/eventRouter';
import { scheduleFlushCommit } from '../perfMetrics';
import { stateRoot } from '../../state/app';

// The store's own bookkeeping: the conversation patches, the run and
// interact indexes, the submission lookup and the stream queue live in
// one closure so every block of the split reads the same state. S4 moved
// these declarations out of store.ts (move only) - the bodies below are
// the ones that sat inside the zustand factory, and they still read
// `set` / `get` by those names.
//
// Two seams had to be rewritten rather than moved verbatim: the stream
// queue reaches the event sink through `setEventSink` (the sink is built
// later in the factory body than this module), and handleEvent buffers a
// delta through `enqueueStreamEvent` instead of touching the queue.

export function createConvCore(api: StoreApi<StoreState>) {
  const set = api.setState;
  const get = api.getState;

  // Installed by the composition root before anything can flush (a
  // flush runs on a frame or a timer, never during the factory body).
  let eventSink = {} as EventDataSink;
  const setEventSink = (sink: EventDataSink) => {
    eventSink = sink;
  };

  let pendingStreamEvents: UIEvent[] = [];
  // Whether the queue holds nothing but reasoning deltas. Kept on
  // enqueue so the cadence question is answered once per delta instead
  // of by rescunning the whole queue (see stream.ts).

  // Whether the queue holds nothing but reasoning deltas. Kept on
  // enqueue so the cadence question is answered once per delta instead
  // of by rescunning the whole queue (see stream.ts).
  let pendingStreamVisible = false;

  let streamFlushRAF: number | null = null;

  let streamFlushTimer: ReturnType<typeof setTimeout> | null = null;
  // lastStreamFlushAt paces stream commits (see stream.ts for the two
  // cadences); streamFlushDeadline is when the armed handle hands the
  // queue to the store.

  // lastStreamFlushAt paces stream commits (see stream.ts for the two
  // cadences); streamFlushDeadline is when the armed handle hands the
  // queue to the store.
  let lastStreamFlushAt = 0;

  let streamFlushDeadline = 0;
  // Session switches must land on the backend in the same order the
  // user requested them. Without this queue, an older resumeSession
  // can finish after a newer NewChat and move the backend context back
  // to the old session.

  // Session switches must land on the backend in the same order the
  // user requested them. Without this queue, an older resumeSession
  // can finish after a newer NewChat and move the backend context back
  // to the old session.
  let contextSwitchQueue: Promise<void> = Promise.resolve();
  // Workspace switches are applied in order. Older restores ignore
  // their result once a newer switch has been requested.

  const runContextSwitch = <T>(op: () => Promise<T>) => {
    const next = contextSwitchQueue.then(op);
    contextSwitchQueue = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  };
  // updateConv applies one patch to a conversation and settles it (see
  // settleConversation). Everything one event writes belongs in that
  // single update: one event is one store write, which is what the
  // stream flush's flush-to-frame probe measures.

  // updateConv applies one patch to a conversation and settles it (see
  // settleConversation). Everything one event writes belongs in that
  // single update: one event is one store write, which is what the
  // stream flush's flush-to-frame probe measures.
  const updateConv = (id: string, patch: Partial<ConversationState>) =>
    set((state) => {
      const conv = state.conversations[id];
      if (!conv) return state;
      return {
        conversations: {
          ...state.conversations,
          [id]: settleConversation({ ...conv, ...patch }),
        },
      };
    });

  // syncPendingIndex rebuilds the prompt-id -> conversation map for one
  // conversation after its pendingInteracts change. Stream deltas never
  // touch this map, so sidebar/interact selectors stay O(1) instead of
  // scanning every loaded conversation on each token.

  // syncPendingIndex rebuilds the prompt-id -> conversation map for one
  // conversation after its pendingInteracts change. Stream deltas never
  // touch this map, so sidebar/interact selectors stay O(1) instead of
  // scanning every loaded conversation on each token.
  const syncPendingIndex = (conversationID: string) => {
    set((state) => {
      const conv = state.conversations[conversationID];
      const promptIDs = new Set(
        (conv?.pendingInteracts ?? []).map((p) => p.id),
      );
      const pendingPromptConvs = { ...state.pendingPromptConvs };
      let changed = false;
      for (const [promptID, ownerID] of Object.entries(pendingPromptConvs)) {
        if (ownerID !== conversationID) continue;
        if (promptIDs.delete(promptID)) continue;
        delete pendingPromptConvs[promptID];
        changed = true;
      }
      for (const promptID of promptIDs) {
        pendingPromptConvs[promptID] = conversationID;
        changed = true;
      }
      if (!changed) return state;
      return { pendingPromptConvs };
    });
  };

  const clearPendingIndex = (conversationID: string) => {
    set((state) => {
      const pendingPromptConvs = { ...state.pendingPromptConvs };
      let changed = false;
      for (const [promptID, ownerID] of Object.entries(pendingPromptConvs)) {
        if (ownerID !== conversationID) continue;
        delete pendingPromptConvs[promptID];
        changed = true;
      }
      if (!changed) return state;
      return { pendingPromptConvs };
    });
  };

  // ensureConversation returns the conversation, creating a busy shell
  // when it is unknown (a live turn resumed after a frontend reload
  // routes by conversation_id). Returns undefined only when the id is
  // empty.

  // ensureConversation returns the conversation, creating a busy shell
  // when it is unknown (a live turn resumed after a frontend reload
  // routes by conversation_id). Returns undefined only when the id is
  // empty.
  const ensureConversation = (convID: string | undefined) => {
    if (!convID) return undefined;
    let conv = get().conversations[convID];
    if (!conv) {
      set((state) => ({
        conversations: {
          ...state.conversations,
          [convID]: {
            ...emptyConv(),
          },
        },
      }));
      conv = get().conversations[convID];
    }
    return conv;
  };

  // startTurnFor is the shared low-level send: it validates the draft,
  // appends the optimistic user message, and starts one turn in the
  // given conversation. Which submission starts a turn - and what that
  // does to a live or in-flight run - is the submission table's answer
  // (SubmitKind), asked by the callers and again here.

  // turnSubmission answers the machine module's submission table: what
  // this submission may do in the phase the conversation's turn is in
  // (see SUBMISSIONS in the machine module - the guards of its SUBMIT
  // transitions read the same cells). Every path that hands the turn
  // something new asks it before spending an IPC or drawing a row.
  const turnSubmission = (conversationID: string, kind: SubmitKind) =>
    submissionEffect(
      stateRoot.registry.get(conversationID)?.getSnapshot(),
      kind,
    );

  // submissionStarts is the two cells that begin a turn: a quiet turn
  // takes one, and a live run gives it up to a barge.

  // submissionStarts is the two cells that begin a turn: a quiet turn
  // takes one, and a live run gives it up to a barge.
  const submissionStarts = (conversationID: string, kind: SubmitKind) => {
    const effect = turnSubmission(conversationID, kind);
    return effect === 'start' || effect === 'barge';
  };

  const conversationTurnState = (conversationID: string) => {
    const actor = stateRoot.registry.get(conversationID);
    if (!actor) return { name: 'idle' as const };
    const value = actor.getSnapshot().value as { turn: string };
    const context = actor.getSnapshot().context as {
      currentRunID?: string;
      turnStage?: string;
      turnError?: string;
      failureStatus?: string;
      supersededRunID?: string;
    };
    switch (value.turn) {
      case 'starting':
        return {
          name: 'starting' as const,
          supersededRunID: context.supersededRunID,
        };
      case 'running':
        return {
          name: 'running' as const,
          runID: context.currentRunID ?? '',
          stage: context.turnStage ?? '',
        };
      case 'failed':
        return {
          name: 'failed' as const,
          error: context.turnError,
        };
      default:
        return { name: value.turn as 'idle' | 'succeeded' };
    }
  };

  const clearStreamFlushHandles = () => {
    if (streamFlushRAF !== null) cancelAnimationFrame(streamFlushRAF);
    if (streamFlushTimer !== null) clearTimeout(streamFlushTimer);
    streamFlushRAF = null;
    streamFlushTimer = null;
  };

  const flushPendingStreams = () => {
    clearStreamFlushHandles();
    if (pendingStreamEvents.length === 0) return;
    const events = pendingStreamEvents;
    pendingStreamEvents = [];
    pendingStreamVisible = false;
    const started = performance.now();
    for (const ev of coalesceStreamEvents(events)) {
      routeBackendEvent(ev, { root: stateRoot, data: eventSink });
    }
    lastStreamFlushAt = performance.now();
    recordFlushDuration(performance.now() - started);
    // The commit lands on the next frame; perfMetrics turns that into the
    // flush-to-frame number the probe reports.
    scheduleFlushCommit(started);
  };

  const scheduleStreamFlush = () => {
    const flush = () => flushPendingStreams();
    const now = performance.now();
    const interval = streamFlushInterval(
      pendingStreamEvents,
      !pendingStreamVisible,
    );
    const dueAt = lastStreamFlushAt + interval;
    if (streamFlushRAF !== null || streamFlushTimer !== null) {
      // A handle is already armed. Only a queue that wants an earlier
      // commit than that handle carries may pull it in — prose arriving
      // in the middle of a reasoning burst, or a tool call landing on
      // the same beat; a queue that wants a later one never pushes it
      // out, so nothing waiting is delayed past its own cadence.
      if (dueAt >= streamFlushDeadline) return;
      clearStreamFlushHandles();
    }
    if (now >= dueAt) {
      // The queue has been idle: commit on the next frame so the first
      // delta of a burst lands immediately instead of waiting out the
      // interval.
      streamFlushRAF =
        typeof requestAnimationFrame === 'function'
          ? requestAnimationFrame(flush)
          : null;
      if (streamFlushRAF === null) streamFlushTimer = setTimeout(flush, 0);
      streamFlushDeadline = now;
      return;
    }
    streamFlushTimer = setTimeout(flush, dueAt - now);
    streamFlushDeadline = dueAt;
  };

  // retainLiveConversations drops transcripts that are neither focused
  // nor backed by a live run, so merely opening many sessions over time
  // does not grow the in-memory store without bound. Resuming one of
  // those sessions hydrates from the archive again.

  // retainLiveConversations drops transcripts that are neither focused
  // nor backed by a live run, so merely opening many sessions over time
  // does not grow the in-memory store without bound. Resuming one of
  // those sessions hydrates from the archive again.
  const retainLiveConversations = (currentID: string) => {
    const state = get();
    const conversations: Record<string, ConversationState> = {};
    const dropped: string[] = [];
    for (const [id, conv] of Object.entries(state.conversations)) {
      if (id === currentID) {
        conversations[id] = conv;
        continue;
      }
      const actor = stateRoot.registry.get(id);
      const turnValue = actor?.getSnapshot().value as
        { turn: string } | undefined;
      const running =
        turnValue?.turn === 'starting' || turnValue?.turn === 'running';
      const mappedRun = stateRoot.registry.hasRun(id);
      const hasPendingPrompt = conv.pendingInteracts.length > 0;
      if (running || mappedRun || hasPendingPrompt) {
        conversations[id] = conv;
      } else {
        dropped.push(id);
      }
    }
    if (dropped.length === 0) return;
    // The viewers of the dropped conversations go with them: the entry
    // is a handful of tab/diff ids per conversation ever opened, and
    // resuming a session rebuilds it from the store's own defaults.
    const viewers = { ...get().viewers };
    for (const id of dropped) delete viewers[id];
    set(() => ({ conversations, viewers }));
    for (const id of dropped) stateRoot.registry.release(id);
  };

  // reconcileTurnFromArchive replaces a finished live turn with the
  // archived copy. If stream deltas were coalesced too aggressively or
  // the runtime sink detached under load, turn_end repairs the
  // transcript instead of leaving a partial answer visible forever.

  const viewerPatch = (id: string | null, patch: Partial<FileViewerState>) => {
    if (!id) return;
    set((state) => ({
      viewers: {
        ...state.viewers,
        [id]: { ...(state.viewers[id] ?? viewerDefaults()), ...patch },
      },
    }));
  };

  // openMintedSession materializes a conversation the backend already
  // minted: the current pointer moved server-side, so this only drives
  // the focus machine and local shell state without another RPC.

  // openMintedSession materializes a conversation the backend already
  // minted: the current pointer moved server-side, so this only drives
  // the focus machine and local shell state without another RPC.
  const openMintedSession = (
    snapshot: {
      conversation_id: string;
      mode: string;
      think: string;
      model: string;
    },
    request: number,
  ) => {
    stateRoot.sendFocus({
      type: 'OPEN_SUCCEEDED',
      request,
      sessionID: snapshot.conversation_id,
    });
    const focus = stateRoot.focusSnapshot;
    if (
      focus.value !== 'active' ||
      focus.context.sessionID !== snapshot.conversation_id
    ) {
      return;
    }
    const id = snapshot.conversation_id;
    set((state) => ({
      toolsView: null,
      conversations: {
        ...state.conversations,
        [id]: emptyConv({
          mode: snapshot.mode,
          think: snapshot.think,
          model: snapshot.model,
        }),
      },
    }));
    stateRoot.registry.ensure(id, {
      workspaceGeneration: stateRoot.generation(),
      readyEmpty: true,
      workspace: get().workspace,
    });
    retainLiveConversations(id);
  };

  // enqueueStreamEvent is what handleEvent did inline: buffer the delta,
  // note whether anything visible rides along, and pace the commit.
  const enqueueStreamEvent = (ev: UIEvent) => {
    pendingStreamEvents.push(ev);
    if (!pendingStreamVisible && !isReasoningDelta(ev)) {
      pendingStreamVisible = true;
    }
    scheduleStreamFlush();
  };

  return {
    pendingStreamEvents,
    pendingStreamVisible,
    streamFlushRAF,
    streamFlushTimer,
    lastStreamFlushAt,
    streamFlushDeadline,
    contextSwitchQueue,
    runContextSwitch,
    updateConv,
    syncPendingIndex,
    clearPendingIndex,
    ensureConversation,
    turnSubmission,
    submissionStarts,
    conversationTurnState,
    clearStreamFlushHandles,
    flushPendingStreams,
    scheduleStreamFlush,
    retainLiveConversations,
    viewerPatch,
    openMintedSession,
    enqueueStreamEvent,
    setEventSink,
  };
}
