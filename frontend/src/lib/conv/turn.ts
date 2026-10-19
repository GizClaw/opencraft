import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';
import { activeConversationID, createConvCore } from './core';
import i18n from '../../i18n';
import { stateRoot } from '../../state/app';
import type { SubmitKind } from '../../state/machines/conversation';
import { api } from '../api';
import { measureInteraction } from '../perfMetrics';
import type { AttachmentView, StreamPart, TurnMessage } from '../types';
import {
  ConversationState,
  MessageView,
  attachmentPart,
  newID,
  newTurnID,
} from './model';

// The turn itself: how a submission becomes a run, how a superseded run
// is drained, and the actions the UI calls to send, steer, interrupt,
// queue and re-send. S4 moved these out of store.ts (move only).
//
// The actions come back as `actions` (typed against StoreState so the
// arrow parameters keep the types the store's interface gave them by
// context) and the turn helpers as `helpers`, which the event sink still
// calls directly.

export interface ConvTurn {
  actions: Pick<
    StoreState,
    | 'send'
    | 'sendInterrupt'
    | 'steer'
    | 'resendSteer'
    | 'dismissSteer'
    | 'queueInput'
    | 'takeQueued'
    | 'clearQueued'
    | 'forkTurn'
    | 'clearLastFailed'
    | 'replyInteract'
    | 'cancelRun'
  >;
  helpers: {
    beginTurn: (
      convID: string,
      text: string,
      messages: MessageView[],
      attachments: AttachmentView[],
      submit: SubmitKind,
    ) => Promise<void>;
    drainQueued: (convID: string) => void;
    markFailedSend: (convID: string, error: string) => void;
    startTurnFor: (
      convID: string,
      text: string,
      attachments: AttachmentView[],
      submit: SubmitKind,
    ) => Promise<boolean>;
  };
}

export function createConvTurn(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
): ConvTurn {
  const set = store.setState;
  const get = store.getState;
  const {
    conversationTurnState,
    runContextSwitch,
    submissionStarts,
    syncPendingIndex,
    turnSubmission,
    updateConv,
  } = core;

  const startTurnFor = async (
    convID: string,
    text: string,
    attachments: AttachmentView[],
    submit: SubmitKind,
  ): Promise<boolean> => {
    const trimmed = text.trim();
    if (
      (!trimmed && attachments.length === 0) ||
      !convID ||
      !get().configured
    ) {
      return false;
    }
    const conv = get().conversations[convID];
    if (!conv) return false;
    // A start the table does not allow here would leave the machine
    // behind the turn the RPC is about to begin.
    if (!submissionStarts(convID, submit)) return false;
    const messages = [
      ...conv.messages,
      {
        id: newID('msg'),
        role: 'user' as const,
        text: trimmed,
        items: [],
        attachments,
      },
    ];
    await beginTurn(convID, trimmed, messages, attachments, submit);
    return true;
  };

  // drainQueued sends the single staged draft for a conversation. It
  // is invoked when that conversation's turn reaches its terminal
  // event (Tab queue) or, for interrupt=true, as soon as the awaited
  // run starts. The draft's own kind picks the table row - an Enter
  // waits to barge in, a Tab draft takes over the turn its run left
  // behind - and a draft the table cannot fire here stays staged.
  const drainQueued = (convID: string) => {
    const queued = get().conversations[convID]?.queued;
    if (!queued) return;
    const kind: SubmitKind = queued.interrupt ? 'interrupt' : 'drain';
    if (!submissionStarts(convID, kind)) return;
    updateConv(convID, { queued: undefined });
    void startTurnFor(convID, queued.text, queued.attachments, kind);
  };

  // markFailedSend marks the live turn entry of a send that never
  // produced a run. Used when a failed barge-in leaves the previous
  // run running: the conversation has to resume that run, so the only
  // place to surface the failed send is its own turn artifact.
  const markFailedSend = (convID: string, error: string) => {
    set((state) => {
      const conv = state.conversations[convID];
      if (!conv || conv.turnArtifacts.length === 0) return state;
      const list = conv.turnArtifacts;
      const idx = list.length - 1;
      return {
        conversations: {
          ...state.conversations,
          [convID]: {
            ...conv,
            turnArtifacts: [
              ...list.slice(0, idx),
              {
                ...list[idx],
                status: 'failed',
                error,
                finishedAt: new Date().toISOString(),
              },
              ...list.slice(idx + 1),
            ],
          },
        },
      };
    });
  };

  const beginTurn = async (
    convID: string,
    text: string,
    messages: MessageView[],
    attachments: AttachmentView[],
    submit: SubmitKind,
  ) => {
    const conv = get().conversations[convID];
    // The turn belongs to the workspace that owns the conversation,
    // not to the one on screen: a Tab/Enter draft drains on the
    // terminal event of the turn it waited for, which can land long
    // after the user switched workspaces. The actor records the
    // workspace the conversation was opened in; a conversation without
    // one is being minted right here, so the active workspace owns it.
    const workspace = stateRoot.workspaceOf(convID) || get().workspace;
    const requestedAt = new Date().toISOString();
    // Keep existing turn strips that still own messages in the new
    // transcript, then open a new live turn entry at the user message
    // just appended.
    const turnArtifacts = [
      ...conv.turnArtifacts.filter((t) => t.start < messages.length),
      {
        id: newTurnID(),
        start: messages.length - 1,
        docs: [],
        requestedAt,
      },
    ];
    updateConv(convID, {
      messages,
      turnArtifacts,
    });
    const startingActor = stateRoot.registry.ensure(convID, {
      workspaceGeneration: stateRoot.generation(),
      workspace,
    });
    startingActor?.send({ type: 'SUBMIT', kind: submit });
    try {
      const parts: StreamPart[] = [];
      if (text) parts.push({ type: 'text', text });
      for (const att of attachments) {
        parts.push(attachmentPart(att));
      }
      const wire: TurnMessage = { role: 'user', content: { parts } };
      const start = await api.startTurn(convID, wire, workspace);
      const list = get().conversations[convID].turnArtifacts;
      const liveIdx = list.length - 1;
      const turnArtifacts =
        liveIdx >= 0
          ? [
              ...list.slice(0, liveIdx),
              {
                ...list[liveIdx],
                runID: start.run_id,
                requestedAt: start.requested_at || list[liveIdx].requestedAt,
                startedAt: start.started_at || new Date().toISOString(),
              },
              ...list.slice(liveIdx + 1),
            ]
          : list;
      // The run is bound where its id becomes known: the index is what
      // routes its deltas — and the terminal event that stamps the
      // strip — back to this conversation.
      stateRoot.registry.bindRun(start.run_id, convID);
      set((state) => ({
        conversations: {
          ...state.conversations,
          [convID]: {
            ...state.conversations[convID],
            turnArtifacts,
          },
        },
      }));
      startingActor?.send({ type: 'RUN_STARTED', runID: start.run_id });
      void get().loadSessions();
      // An Enter pressed while the previous send was still "starting"
      // staged interrupt=true. The run just started, so fire it now;
      // the replacement barges in through the same drain path.
      if (get().conversations[convID]?.queued?.interrupt) {
        drainQueued(convID);
      }
    } catch (err) {
      const conv = get().conversations[convID];
      if (!conv) return;
      const error = String(err);
      const snapshot = startingActor?.getSnapshot();
      const turnValue = (snapshot?.value as { turn?: string } | undefined)
        ?.turn;
      const context = (snapshot?.context ?? {}) as {
        supersededRunID?: string;
      };
      const bargeInFailed =
        turnValue === 'starting' && context.supersededRunID !== undefined;
      if (turnValue === 'starting') {
        // The send failed. A barge-in's failure is the actor's to sort
        // out — it recorded the superseded run's terminal event if one
        // arrived while this send was starting, and the run may still
        // be live otherwise, so both outs (resume watching it, absorb
        // its ending) are START_FAILED's (see the machine's `starting`
        // state). A plain send has no run behind it; its failure is
        // reported by START_FAILED's fallback branch without touching
        // the ended-run memory — the synthetic `TURN_ENDED` with an
        // empty run id this replaces is what used to wipe it.
        startingActor?.send({ type: 'START_FAILED', error });
      }
      if (bargeInFailed) {
        markFailedSend(convID, error);
        // Tab drafts stay staged for the resumed run; interrupt
        // intents aimed at the start that just failed are dropped.
        const queued = conv.queued;
        if (queued?.interrupt) {
          updateConv(convID, { queued: undefined });
        }
        return;
      }
      // A staged draft no longer has a run to wait for. Interrupt
      // intents were aimed at the failed start, so drop them; Tab
      // queues still fire after the failed send settles.
      const queued = get().conversations[convID]?.queued;
      if (queued?.interrupt) {
        updateConv(convID, { queued: undefined });
      } else if (queued) {
        drainQueued(convID);
      }
    }
  };

  return {
    actions: {
      send: (text, attachments = []) =>
        measureInteraction('send', async () => {
          const trimmed = text.trim();
          const state = get();
          const convID = activeConversationID();
          const conv = convID ? state.conversations[convID] : undefined;
          if (
            (!trimmed && attachments.length === 0) ||
            !convID ||
            !conv ||
            !state.configured
          ) {
            return;
          }
          // A reply is refused while the turn is live: the table's send
          // column has 'start' only for a quiet turn.
          if (!submissionStarts(convID, 'send')) return;
          // Any fresh manual send supersedes a draft that is still staged
          // (for example after the turn it was queued behind failed).
          updateConv(convID, { queued: undefined });
          await startTurnFor(convID, text, attachments, 'send');
        }),

      sendInterrupt: (text, attachments = []) =>
        measureInteraction('send', async () => {
          const trimmed = text.trim();
          const state = get();
          const convID = activeConversationID();
          const conv = convID ? state.conversations[convID] : undefined;
          if (
            (!trimmed && attachments.length === 0) ||
            !convID ||
            !conv ||
            !state.configured
          ) {
            return false;
          }
          const effect = turnSubmission(convID, 'interrupt');
          if (effect === 'stage') {
            // The awaited run has not started yet, so there is no run id
            // to interrupt: park the input and fire it as a barge-in the
            // moment it starts (the drain takes it from there).
            updateConv(convID, {
              queued: { text: trimmed, attachments, interrupt: true },
            });
            return true;
          }
          if (effect === 'refuse') return false;
          // Enter is "answer me now": drop anything staged with Tab and
          // start immediately. While a turn is running the engine
          // interrupts it; otherwise this is a normal send.
          updateConv(convID, { queued: undefined });
          await startTurnFor(convID, text, attachments, 'interrupt');
          return true;
        }),

      steer: async (text, attachments = []) => {
        const trimmed = text.trim();
        const state = get();
        const convID = activeConversationID();
        const conv = convID ? state.conversations[convID] : undefined;
        if (
          !trimmed ||
          attachments.length > 0 ||
          !convID ||
          !conv ||
          !state.configured
        ) {
          // Steer carries text only; a draft with attachments has to wait
          // for its own turn.
          return false;
        }
        // A steer goes into the live run itself: the table refuses it in
        // every other phase, and the run id is what the RPC addresses.
        if (turnSubmission(convID, 'steer') !== 'steer') return false;
        const turn = conversationTurnState(convID);
        if (turn.name !== 'running') return false;
        const runID = turn.runID;
        const row: MessageView = {
          id: newID('msg'),
          role: 'user',
          text: trimmed,
          items: [],
          attachments: [],
          // The row is stamped before the RPC so the transcript shows it as
          // an interjection from the first paint: it never looked like a
          // new turn, and turn_end only settles which of the two final
          // states it reaches.
          steer: 'pending',
        };
        // Draw the optimistic row and register it before the RPC: turn_end
        // can settle while the submission is still in flight, and it
        // classifies rows by what is registered at that moment.
        updateConv(convID, {
          messages: [...conv.messages, row],
          steerSent: [
            ...(conv.steerSent ?? []),
            { runID, messageID: row.id, text: trimmed },
          ],
        });
        try {
          await api.steerTurn(runID, trimmed);
          return true;
        } catch {
          const convNow = get().conversations[convID];
          const drawn = (convNow?.messages ?? []).find((m) => m.id === row.id);
          const patch: Partial<ConversationState> = {
            steerSent: (convNow?.steerSent ?? []).filter(
              (s) => s.messageID !== row.id,
            ),
          };
          // Only the row still waiting for a boundary is pulled back out:
          // a row turn_end settled belongs to the conversation (the archive
          // has the text, or the row keeps it as undelivered), and a row a
          // rebuild replaced is not this submission's to remove.
          const waiting = drawn?.steer === 'pending';
          if (waiting) {
            patch.messages = (convNow?.messages ?? []).filter(
              (m) => m.id !== row.id,
            );
          }
          // Whether the text landed somewhere is a question about the
          // text, not about the registration. turn_end settles a steer by
          // stamping the row and dropping its registration, but a rebuild
          // (retryTranscript, a hydrate) drops the registrations along
          // with the shell it replaced, so a missing one proves nothing.
          // A row still pending is proof that nothing took the text; a
          // row the turn stamped proves the conversation owns it; and a
          // row a rebuild replaced is archived text only when the rebuilt
          // transcript holds it as a settled steer.
          const settled = drawn
            ? drawn.steer !== 'pending'
            : (convNow?.messages ?? []).some(
                (m) =>
                  m.role === 'user' &&
                  m.text === trimmed &&
                  (m.steer === 'delivered' || m.steer === 'undelivered'),
              );
          updateConv(convID, patch);
          if (settled) {
            // The run's turn_end settled while this submission was in
            // flight; the conversation owns the text now (either the
            // archive has it or the row is marked undelivered).
            return true;
          }
          if (turnSubmission(convID, 'send') === 'start') {
            // The run ended between submit and rejection: a normal send is
            // what the user meant, so start one instead of losing the text.
            await startTurnFor(convID, trimmed, [], 'send');
            return true;
          }
          // The turn (or its replacement) is still live: keep the draft
          // and say what happened instead of silently changing the intent.
          get().toast(i18n.t('chat.steerRejected'), 'warning');
          return false;
        }
      },

      resendSteer: async (id) => {
        const convID = activeConversationID();
        const conv = convID ? get().conversations[convID] : undefined;
        if (!convID || !conv) return;
        const row = conv.messages.find(
          (m) => m.id === id && m.steer === 'undelivered',
        );
        if (!row) return;
        // The row takes whichever shape the turn can take (the table's
        // resend column): a quiet turn takes it as a turn of its own
        // ('start'), a live one stages it the way a Tab draft would
        // ('stage'). A refused row stays where it is.
        const effect = turnSubmission(convID, 'resend');
        if (effect === 'refuse') return;
        // Only drop the row once the draft is actually staged.
        if (effect === 'stage' && !get().queueInput(row.text, [])) return;
        updateConv(convID, {
          messages: conv.messages.filter((m) => m.id !== id),
        });
        if (effect === 'start')
          await startTurnFor(convID, row.text, [], 'send');
      },

      dismissSteer: (id) => {
        const convID = activeConversationID();
        const conv = convID ? get().conversations[convID] : undefined;
        if (!convID || !conv) return;
        updateConv(convID, {
          messages: conv.messages.filter(
            (m) => !(m.id === id && m.steer === 'undelivered'),
          ),
        });
      },

      queueInput: (text, attachments = []) => {
        const trimmed = text.trim();
        const state = get();
        const convID = activeConversationID();
        const conv = convID ? state.conversations[convID] : undefined;
        if (
          (!trimmed && attachments.length === 0) ||
          !convID ||
          !conv ||
          !state.configured
        ) {
          return false;
        }
        // The table's queue column stages a draft exactly when there is
        // a live turn to wait for.
        if (turnSubmission(convID, 'queue') !== 'stage') return false;
        // A single queue slot: a later Tab replaces the staged draft.
        updateConv(convID, {
          queued: { text: trimmed, attachments, interrupt: false },
        });
        return true;
      },

      takeQueued: () => {
        const convID = activeConversationID();
        const conv = convID ? get().conversations[convID] : undefined;
        if (!convID || !conv?.queued) return undefined;
        const staged = conv.queued;
        updateConv(convID, { queued: undefined });
        return staged;
      },

      clearQueued: () => {
        const convID = activeConversationID();
        if (convID) updateConv(convID, { queued: undefined });
      },

      forkTurn: async (runID) => {
        const convID = activeConversationID();
        if (!convID || !runID) return;
        try {
          const newID = await runContextSwitch(() =>
            api.forkTurn(convID, runID),
          );
          if (!newID) return;
          await get().resume(newID);
          void get().loadSessions();
        } catch (err) {
          set({ statusText: String(err) });
        }
      },

      clearLastFailed: () => {
        const convID = activeConversationID();
        if (!convID) return;
        const turn = conversationTurnState(convID);
        if (turn.name === 'failed') {
          stateRoot.registry.get(convID)?.send({ type: 'DISMISS_FAILURE' });
        }
      },

      replyInteract: async (id, req) => {
        try {
          await api.replyPrompt(id, req);
        } catch (err) {
          // Keep the card on failure: the backend prompt is still
          // pending, so removing it would make the interaction
          // unreachable.
          set({ statusText: String(err) });
          return;
        }
        const convID = get().pendingPromptConvs[id];
        const conv = convID ? get().conversations[convID] : undefined;
        if (conv && conv.pendingInteracts.some((p) => p.id === id)) {
          updateConv(convID, {
            pendingInteracts: conv.pendingInteracts.filter((p) => p.id !== id),
          });
          syncPendingIndex(convID);
        }
      },

      cancelRun: async () => {
        const convID = activeConversationID();
        const turn = convID ? conversationTurnState(convID) : undefined;
        let runID = '';
        if (turn?.name === 'running') {
          runID = turn.runID;
        } else if (turn?.name === 'starting' && turn.supersededRunID) {
          // A barge-in is waiting for the superseded run to finalize.
          // Force-cancelling it lets the pending replacement start.
          runID = turn.supersededRunID;
        }
        if (!runID) return;
        try {
          await api.cancelTurn(runID);
        } catch (err) {
          // The superseded run may already have settled while the
          // replacement was starting; that is the happy path, so a
          // "turn not found" failure must not surface as an error.
          if (
            !/not found|not active|already (ended|finished)/i.test(String(err))
          ) {
            // Surface real cancel failures instead of leaving the UI
            // running silently; a real cancel settles via turn_end.
            set({ statusText: String(err) });
          }
        }
      },
    },
    helpers: { beginTurn, drainQueued, markFailedSend, startTurnFor },
  };
}
