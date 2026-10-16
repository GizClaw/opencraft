import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';
import { createConvCore } from './core';
import type { ConvTurn } from './turn';
import { createConvPaging } from './paging';
import { createWorkspaceGlue } from './workspace';
import i18n from '../../i18n';
import { stateRoot } from '../../state/app';
import { EventDataSink } from '../../state/eventRouter';
import { api } from '../api';
import { UIEventType } from '../events';
import type {
  AutomationRun,
  ConfigStatus,
  InteractDTO,
  StreamDelta,
  UsageDTO,
} from '../types';
import {
  artifactTurnIndex,
  liveRunTurn,
  mergeTurnDoc,
  normalizeTurnStatus,
} from './artifacts';
import {
  SteerState,
  capUndeliveredSteers,
  emptyConv,
  newID,
  newTurnID,
  settleConversation,
  settleSteerDelivery,
} from './model';
import { applyStream } from './stream';

// The event sink: what a backend event writes when the router has
// decided it may land. S4 moved this out of store.ts (move only) - the
// bodies below are the ones that sat in the factory, and they still read
// `set` / `get` by those names.

export function createEventSink(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
  turn: ConvTurn['helpers'],
  paging: ReturnType<typeof createConvPaging>,
  ws: ReturnType<typeof createWorkspaceGlue>,
): EventDataSink {
  const set = store.setState;
  const get = store.getState;
  const { ensureConversation, syncPendingIndex, updateConv } = core;
  const { drainQueued } = turn;
  const { syncTranscriptTail } = paging;
  const { applyWorkspaceSwitch } = ws;

  const eventDataSink: EventDataSink = {
    writeConversationData: (conversationID, ev) => {
      switch (ev.type) {
        case UIEventType.stream: {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            delta: StreamDelta;
          };
          const runID = data.run_id;
          if (!runID) break;
          const actor = stateRoot.registry.get(conversationID);
          if (actor) {
            const snapshot = actor.getSnapshot();
            const value = snapshot.value as unknown as { turn: string };
            const context = snapshot.context as {
              currentRunID?: string;
              lastEndedRunID?: string;
              supersededRunID?: string;
            };
            if (context.supersededRunID && runID === context.supersededRunID) {
              // A barge-in is replacing this run; its late deltas must
              // never land after the replacement's user message.
              break;
            }
            if (value.turn === 'running') {
              // A delayed delta from an earlier run must never be
              // folded into a newer run's live transcript.
              if (context.currentRunID && runID !== context.currentRunID) {
                break;
              }
            } else if (
              (value.turn === 'starting' || value.turn === 'idle') &&
              runID === context.lastEndedRunID
            ) {
              // The machine already ignores a stream for the last ended
              // run; mirror it here so data never reaches the renderer.
              break;
            }
          }
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          // The run's rows start at its own strip, opened here when the
          // conversation holds none (a run this window only met on the
          // wire — see liveRunTurn). The delta folds into the rows at or
          // after that seam, and into no row above it: those belong to
          // another turn.
          const live = liveRunTurn(conv, runID);
          // An adopted run is bound with the strip it opens: a barge-in
          // over it has to see it as live (the machine records the
          // superseded run's ending, the registry is what routes it),
          // and the turn_end that releases the run is the one that
          // stamps that strip.
          if (live.adopted) {
            stateRoot.registry.bindRun(runID, conversationID);
          }
          updateConv(conversationID, {
            turnArtifacts: live.turnArtifacts,
            messages: applyStream(conv.messages, data.delta, live.turn.start),
          });
          break;
        }
        case UIEventType.interact: {
          const spec = ev.data as InteractDTO;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          if (!conv.pendingInteracts.some((p) => p.id === spec.id)) {
            updateConv(conversationID, {
              pendingInteracts: [...conv.pendingInteracts, spec],
            });
          }
          syncPendingIndex(conversationID);
          break;
        }
        case UIEventType.resolved: {
          const data = ev.data as { id: string };
          const conv = get().conversations[conversationID];
          if (conv?.pendingInteracts.some((p) => p.id === data.id)) {
            updateConv(conversationID, {
              pendingInteracts: conv.pendingInteracts.filter(
                (p) => p.id !== data.id,
              ),
            });
            syncPendingIndex(conversationID);
          }
          break;
        }
        case UIEventType.artifact: {
          const data = ev.data as {
            run_id?: string;
            path?: string;
            bytes?: number;
          };
          if (!data.path) break;
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const list = conv.turnArtifacts;
          const idx = artifactTurnIndex(list, data.run_id);
          if (idx < 0) break;
          const docs = mergeTurnDoc(list[idx].docs, data.path, data.bytes ?? 0);
          updateConv(conversationID, {
            turnArtifacts: [
              ...list.slice(0, idx),
              { ...list[idx], docs },
              ...list.slice(idx + 1),
            ],
          });
          break;
        }
        case UIEventType.steerPending: {
          // A round boundary drained the run's steer queue: the rows it
          // carried are delivered now, while the turn keeps running.
          const data = ev.data as {
            run_id?: string;
            steer_pending?: number;
          };
          if (!data.run_id || typeof data.steer_pending !== 'number') break;
          const conv = get().conversations[conversationID];
          if (!conv) break;
          const patch = settleSteerDelivery(
            conv,
            data.run_id,
            data.steer_pending,
          );
          if (patch) updateConv(conversationID, patch);
          break;
        }
        case UIEventType.turnEnd: {
          const data = ev.data as {
            run_id?: string;
            status: string;
            error?: string;
            interrupt_cause?: string;
            error_kind?: string;
            request_id?: string;
            response_id?: string;
            finished_at?: string;
            duration_ms?: number;
            compaction?: {
              folds: number;
              failures?: number;
              notified?: boolean;
            };
            // steer_pending counts the steered messages this turn ended
            // without delivering. A zero is a real zero (everything made
            // it); null — or the field missing entirely, which a producer
            // this build does not know about would send — means the
            // backend could not read the count, and every steered row for
            // the run is marked undelivered instead: the transcript rows
            // are the only copy of that text and archive reconciliation
            // rebuilds the turn from the archive, which never saw them.
            steer_pending?: number | null;
          };
          const conv = ensureConversation(conversationID);
          if (!conv) break;
          const finishedAt = data.finished_at || new Date().toISOString();
          const endingRun = data.run_id ?? '';
          // Which run the conversation is on right now. The actor has not
          // consumed this terminal event yet (the data layer runs first),
          // so what this names is either the ending run itself or the
          // replacement a barge-in started — and the rows handed to that
          // replacement are still waiting for a boundary of their own.
          const liveRunID = (
            stateRoot.registry.get(conversationID)?.getSnapshot().context as
              { currentRunID?: string } | undefined
          )?.currentRunID;
          // The run is released with its terminal event: from here the
          // transcript holds everything the run will ever report.
          if (endingRun) stateRoot.registry.releaseRun(endingRun);
          set((state) => {
            const conv = state.conversations[conversationID];
            if (!conv) return state;
            const turnArtifacts = conv.turnArtifacts.map((t) =>
              t.runID && t.runID === data.run_id
                ? {
                    ...t,
                    finishedAt,
                    durationMs:
                      data.duration_ms !== undefined
                        ? data.duration_ms
                        : t.durationMs,
                    status: normalizeTurnStatus(data.status) ?? t.status,
                    error: data.error ?? t.error,
                    interruptCause: data.interrupt_cause ?? t.interruptCause,
                    errorKind: data.error_kind ?? t.errorKind,
                    requestID: data.request_id ?? t.requestID,
                    responseID: data.response_id ?? t.responseID,
                    compaction: data.compaction ?? t.compaction,
                  }
                : t,
            );
            // Steer bookkeeping: the run's steered messages are FIFO, so
            // the undelivered ones are the newest entries registered for
            // it. Their text never reached the archive, so the rows stay
            // in the transcript — marked as undelivered, with their
            // resend/discard actions — instead of being dropped by the
            // archive reconciliation that follows this event.
            const tracked = (conv.steerSent ?? []).filter(
              (s) => s.runID === endingRun,
            );
            const rawPending = data.steer_pending;
            const pending =
              rawPending === null || rawPending === undefined
                ? tracked.length
                : Math.max(0, Math.floor(rawPending));
            const undelivered = pending > 0 ? tracked.slice(-pending) : [];
            const states = new Map<string, SteerState>();
            for (const s of tracked) states.set(s.messageID, 'delivered');
            for (const s of undelivered) states.set(s.messageID, 'undelivered');
            // A row handed to a run that is neither the one that just
            // ended nor the live one can never be classified anymore: its
            // terminal event was dropped, or a barge-in replaced the turn
            // before that run reported. Settling it as undelivered is the
            // honest answer — the archive keeps the text of everything a
            // boundary did take, and a rebuild retags those rows — where
            // leaving it pending would promise a boundary that never
            // comes.
            for (const s of conv.steerSent ?? []) {
              if (s.runID === endingRun || s.runID === liveRunID) continue;
              states.set(s.messageID, 'undelivered');
            }
            const messages =
              states.size > 0
                ? capUndeliveredSteers(
                    conv.messages.map((m) => {
                      const next = states.get(m.id);
                      return next ? { ...m, steer: next } : m;
                    }),
                  )
                : conv.messages;
            return {
              conversations: {
                ...state.conversations,
                [conversationID]: settleConversation({
                  ...conv,
                  messages,
                  turnArtifacts,
                  steerSent: (conv.steerSent ?? []).filter(
                    (s) => !states.has(s.messageID),
                  ),
                }),
              },
            };
          });
          void get().loadSessions();
          // A Tab-staged draft fires only when the run it was waiting
          // for completes. Drain before the actor applies TURN_ENDED:
          // the drain column of the submission table marks the
          // just-ended run as superseded, so the pending terminal event
          // stays inert and the next turn starts cleanly. Failed/cancelled/aborted endings keep the
          // draft staged (the composer shows how to send it), and a
          // terminal event from a superseded run must never drain a
          // draft that is waiting for the replacement.
          const staged = get().conversations[conversationID]?.queued;
          if (staged && !staged.interrupt && data.status === 'completed') {
            const actor = stateRoot.registry.get(conversationID);
            const snapshot = actor?.getSnapshot();
            const turn = (snapshot?.value as { turn?: string } | undefined)
              ?.turn;
            const context = (snapshot?.context ?? {}) as {
              currentRunID?: string;
            };
            const isWatchedRun =
              turn === 'running' && context.currentRunID === data.run_id;
            if (!isWatchedRun) break;
            drainQueued(conversationID);
          }
          break;
        }
        case UIEventType.automationRunStarted: {
          const data = ev.data as {
            run_id?: string;
            conversation_id?: string;
            message?: string;
          };
          const runID = data.run_id;
          const at = new Date().toISOString();
          if (runID) {
            // A run the UI never sent is still a run this conversation
            // owns: binding it is what routes its deltas and the
            // turn_end that stamps its strip.
            stateRoot.registry.bindRun(runID, conversationID);
          }
          set((state) => {
            const existing = state.conversations[conversationID];
            const conv = existing ?? emptyConv();
            // An event for a run the transcript already holds adds the
            // mapping and nothing else.
            const known =
              runID !== undefined &&
              conv.turnArtifacts.some((t) => t.runID === runID);
            if (!runID || known) {
              if (existing) return {};
              return {
                conversations: {
                  ...state.conversations,
                  [conversationID]: conv,
                },
              };
            }
            // A run the UI did not start opens its own turn the same way
            // beginTurn opens a turn the user sent: the run's user row
            // (the task's message, which is exactly what the host
            // archives as this turn's user message) plus the strip that
            // owns it. Both halves matter — the row is what keeps the
            // run's live deltas (lastAssistant appends to the trailing
            // assistant row, so without it they land on the previous
            // turn) and its files (the strip's footer only draws for a
            // turn that owns rows) with the turn the archive will draw,
            // and the strip's run id is what the artifact and turn_end
            // paths look the turn up by.
            const messages = [
              ...conv.messages,
              {
                id: newID('msg'),
                role: 'user' as const,
                text: data.message ?? '',
                items: [],
                attachments: [],
              },
            ];
            return {
              conversations: {
                ...state.conversations,
                [conversationID]: {
                  ...conv,
                  messages,
                  turnArtifacts: [
                    ...conv.turnArtifacts,
                    {
                      id: newTurnID(),
                      start: messages.length - 1,
                      docs: [],
                      runID,
                      requestedAt: at,
                      startedAt: at,
                    },
                  ],
                },
              },
            };
          });
          break;
        }
      }
    },

    writeGlobalData: (ev) => {
      switch (ev.type) {
        case UIEventType.ready: {
          const data = ev.data as ConfigStatus;
          const workChanged = data.work_dir !== get().workspace;
          if (workChanged) {
            applyWorkspaceSwitch(data.work_dir);
          }
          void get().loadSessions();
          void get().loadAutomations();
          set((state) => ({
            status: data,
            configured: !data.needed,
            configOpen: workChanged ? false : state.configOpen,
            fatal: null,
          }));
          void api
            .modelOptions()
            .then((modelOptions) => set({ modelOptions }))
            .catch(() => {
              // model list refresh is best-effort; the UI keeps the
              // last known options until the next ready event.
            });
          void get().refreshAgents();
          void get().loadWorkspaces();
          break;
        }
        case UIEventType.fatal:
          set({ fatal: (ev.data as { error: string }).error ?? '' });
          break;
        case UIEventType.status:
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case UIEventType.usage:
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case UIEventType.managedRestored: {
          const ids = ((ev.data as { ids?: string[] }).ids ?? []).filter(
            (id) => id,
          );
          if (ids.length > 0) {
            get().toast(
              i18n.t('config.managedRestored', { plugins: ids.join(', ') }),
            );
          }
          break;
        }
      }
    },

    refreshSessionList: () => void get().loadSessions(),
    sessionUpdated: (id) => void syncTranscriptTail(id),
    refreshAutomations: () => void get().loadAutomations(),
    refreshAutomationRuns: (ev) => {
      const data = ev.data as AutomationRun;
      if (data?.task_id) void get().loadAutomationRuns(data.task_id);
    },
    conversationForRunID: (runID) =>
      stateRoot.registry.conversationForRun(runID),
    pendingInteractConversation: (promptID) =>
      get().pendingPromptConvs[promptID],
    activeWorkspace: () => get().workspace,
  };
  core.setEventSink(eventDataSink);
  return eventDataSink;
}
