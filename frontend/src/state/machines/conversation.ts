import { assign, createActor, createMachine } from 'xstate';

export interface ConversationInput {
  conversationID: string;
  workspaceGeneration?: number;
  readyEmpty?: boolean;
  workspace?: string;
}

interface ConversationContext {
  id: string;
  workspaceGeneration: number;
  workspace?: string;
  lastHydrateRequest?: number;
  currentRunID?: string;
  // supersededRunID names the run a barge-in send is replacing. While
  // the replacement is still starting, streams and the terminal event
  // from the superseded run must not move the conversation state.
  supersededRunID?: string;
  // supersededEnd records the terminal event of that superseded run when
  // it lands while the replacement is still starting: whether the old
  // run is still alive is a question only the run's own events answer,
  // and this machine is what sees them. A failed start reads it here
  // (see the `starting` state) rather than the store reconstructing the
  // answer from a run index and an artifact strip.
  supersededEnd?: {
    status: TurnEndStatus;
    error?: string;
    errorKind?: string;
  };
  lastEndedRunID?: string;
  turnStage?: string;
  failureStatus?: 'failed' | 'aborted' | 'canceled' | 'interrupted';
  // failureErrorKind is the structured class of the failure (the
  // harness's "timeout", an inference kind). It carries the one split
  // status alone cannot: the engine reports a deadline and a user stop
  // as the same `canceled`.
  failureErrorKind?: string;
  turnError?: string;
  transcriptError?: string;
  deletedAt?: string;
  emptyTranscript?: boolean;
}

export type ConversationEvent =
  | { type: 'HYDRATE_REQUESTED'; request: number; generation: number }
  | { type: 'HYDRATE_OK'; request: number; generation: number; empty: boolean }
  | { type: 'HYDRATE_FAIL'; request: number; generation: number; error: string }
  | { type: 'NEW_CHAT_READY' }
  | { type: 'SEND_STARTED' }
  | { type: 'START_FAILED'; error: string }
  | { type: 'RUN_STARTED'; runID: string }
  | { type: 'STREAM'; runID: string; stage?: string }
  | {
      type: 'TURN_ENDED';
      runID: string;
      status: TurnEndStatus;
      error?: string;
      errorKind?: string;
    }
  | { type: 'DISMISS_FAILURE' }
  | { type: 'SESSION_DELETED'; deletedAt?: string };

type TurnEndStatus =
  'completed' | 'failed' | 'aborted' | 'canceled' | 'interrupted';

export const conversationMachine = createMachine({
  id: 'conversation',
  type: 'parallel',
  types: {} as {
    context: ConversationContext;
    events: ConversationEvent;
    input: ConversationInput;
  },
  context: ({ input }) => ({
    id: input.conversationID,
    workspaceGeneration: input.workspaceGeneration ?? 0,
    workspace: input.workspace,
    emptyTranscript: input.readyEmpty ?? false,
  }),
  states: {
    lifecycle: {
      initial: 'alive',
      states: {
        alive: {
          on: {
            SESSION_DELETED: {
              target: 'deleted',
              actions: assign({
                deletedAt: ({ event }) =>
                  event.deletedAt ?? new Date().toISOString(),
              }),
            },
          },
        },
        deleted: {},
      },
    },
    transcript: {
      initial: 'unloaded',
      states: {
        unloaded: {
          on: {
            NEW_CHAT_READY: {
              guard: ({ context }) => !context.deletedAt,
              target: 'ready',
              actions: assign({ emptyTranscript: () => true }),
            },
            HYDRATE_REQUESTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'loading',
              actions: assign({
                lastHydrateRequest: ({ event }) => event.request,
                workspaceGeneration: ({ event }) => event.generation,
              }),
            },
          },
        },
        loading: {
          on: {
            HYDRATE_REQUESTED: {
              guard: ({ context }) => !context.deletedAt,
              actions: assign({
                lastHydrateRequest: ({ event }) => event.request,
                workspaceGeneration: ({ event }) => event.generation,
              }),
            },
            HYDRATE_OK: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.request === context.lastHydrateRequest &&
                event.generation === context.workspaceGeneration,
              target: 'ready',
              actions: assign({
                emptyTranscript: ({ event }) => event.empty,
              }),
            },
            HYDRATE_FAIL: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.request === context.lastHydrateRequest &&
                event.generation === context.workspaceGeneration,
              target: 'failed',
              actions: assign({
                transcriptError: ({ event }) => event.error,
              }),
            },
          },
        },
        ready: {},
        failed: {
          on: {
            HYDRATE_REQUESTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'loading',
              actions: assign({
                lastHydrateRequest: ({ event }) => event.request,
                workspaceGeneration: ({ event }) => event.generation,
              }),
            },
          },
        },
      },
    },
    turn: {
      initial: 'idle',
      states: {
        idle: {
          on: {
            SEND_STARTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'starting',
            },
            // ActiveRun() recovery: a conversation actor created after
            // the run already started receives RUN_STARTED directly.
            RUN_STARTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: () => '',
              }),
            },
            // Frontend reload recovery: the first stream of a live run
            // can restore a conversation whose run started before the
            // UI mounted. A late stream for an already-finished run is
            // ignored via lastEndedRunID.
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                (event.runID !== context.currentRunID ||
                  event.stage !== context.turnStage),
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: ({ event }) => event.stage ?? '',
              }),
            },
            // The other end of the same recovery: a run that this client
            // never watched (a reload adopted it, or a scheduled
            // automation started it) can also finish without a single
            // delta, and its terminal event is the only thing that
            // carries the outcome — status, and the error kind that is
            // the one way to tell a deadline from a user stop. Dropping
            // it here lost the live end of the turn.
            TURN_ENDED: [
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.lastEndedRunID &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: ({ event }) =>
                    event.status === 'failed' ||
                    event.status === 'aborted' ||
                    event.status === 'canceled' ||
                    event.status === 'interrupted'
                      ? event.status
                      : undefined,
                  failureErrorKind: ({ event }) => event.errorKind,
                  turnError: ({ event }) => event.error,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt && event.runID !== context.lastEndedRunID,
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
            ],
          },
        },
        starting: {
          on: {
            // The send that was supposed to replace a running turn
            // failed. If the superseded run is still alive, resume
            // watching it so its streams and terminal event keep
            // driving the conversation; if its terminal event already
            // arrived, absorb that status instead of inventing a
            // fresh failure for the old run.
            START_FAILED: [
              {
                // Nothing recorded: the old run has not reported an
                // ending, so it is still live — watch it again.
                guard: ({ context }) =>
                  !context.deletedAt &&
                  context.supersededRunID !== undefined &&
                  context.supersededEnd === undefined,
                target: 'running',
                actions: assign({
                  currentRunID: ({ context }) => context.supersededRunID,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  turnStage: () => '',
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
              {
                guard: ({ context }) =>
                  !context.deletedAt &&
                  context.supersededRunID !== undefined &&
                  context.supersededEnd?.status === 'completed',
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  lastEndedRunID: ({ context }) => context.supersededRunID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
              {
                guard: ({ context }) =>
                  !context.deletedAt && context.supersededRunID !== undefined,
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  lastEndedRunID: ({ context }) => context.supersededRunID,
                  failureStatus: ({ context }) =>
                    context.supersededEnd?.status === 'failed' ||
                    context.supersededEnd?.status === 'aborted' ||
                    context.supersededEnd?.status === 'canceled' ||
                    context.supersededEnd?.status === 'interrupted'
                      ? context.supersededEnd.status
                      : 'failed',
                  failureErrorKind: ({ context }) =>
                    context.supersededEnd?.errorKind,
                  turnError: ({ context, event }) =>
                    context.supersededEnd?.error ?? event.error,
                }),
              },
            ],
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                event.runID !== context.supersededRunID,
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                supersededEnd: () => undefined,
                turnStage: () => '',
              }),
            },
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                event.runID !== context.supersededRunID &&
                (event.runID !== context.currentRunID ||
                  event.stage !== context.turnStage),
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                supersededEnd: () => undefined,
                turnStage: ({ event }) => event.stage ?? '',
              }),
            },
            TURN_ENDED: [
              {
                // The superseded run's terminal event lands while the
                // replacement is still starting. The run was this
                // conversation's current one when the send began, so
                // the machine is what saw the ending: record it and
                // stay in `starting` — a failed start absorbs it (see
                // START_FAILED), a replacement that does start clears
                // it with the run swap.
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  context.supersededRunID !== undefined &&
                  event.runID === context.supersededRunID,
                actions: assign({
                  supersededEnd: ({ event }) => ({
                    status: event.status,
                    error: event.error,
                    errorKind: event.errorKind,
                  }),
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.supersededRunID &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: ({ event }) =>
                    event.status === 'failed' ||
                    event.status === 'aborted' ||
                    event.status === 'canceled' ||
                    event.status === 'interrupted'
                      ? event.status
                      : undefined,
                  failureErrorKind: ({ event }) => event.errorKind,
                  turnError: ({ event }) => event.error,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt && event.runID !== context.supersededRunID,
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
            ],
          },
        },
        running: {
          on: {
            // A barge-in send starts while the previous turn is still
            // running. The previous run becomes superseded; the engine
            // interrupts it and only starts the replacement after it
            // has been finalized.
            SEND_STARTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'starting',
              actions: assign({
                currentRunID: () => undefined,
                supersededRunID: ({ context }) => context.currentRunID,
                supersededEnd: () => undefined,
                turnStage: () => '',
              }),
            },
            // The replacement's RUN_STARTED can beat the superseded
            // run's terminal event. Swap the current run so late
            // events from the old run stay inert.
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                event.runID !== context.currentRunID,
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                supersededEnd: () => undefined,
                turnStage: () => '',
              }),
            },
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.supersededRunID &&
                event.runID === context.currentRunID &&
                event.stage !== context.turnStage,
              actions: assign({
                turnStage: ({ event }) => event.stage ?? '',
              }),
            },
            TURN_ENDED: [
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID === context.currentRunID &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: ({ event }) =>
                    event.status === 'failed' ||
                    event.status === 'aborted' ||
                    event.status === 'canceled' ||
                    event.status === 'interrupted'
                      ? event.status
                      : undefined,
                  failureErrorKind: ({ event }) => event.errorKind,
                  turnError: ({ event }) => event.error,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt && event.runID === context.currentRunID,
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
            ],
          },
        },
        succeeded: {
          on: {
            SEND_STARTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'starting',
              actions: assign({
                supersededRunID: () => undefined,
              }),
            },
            // A run can also begin while the turn rests on a terminal
            // state, and for the same reasons as the two above: the
            // automation that pinned this conversation starts its turn
            // whenever its schedule fires, and a reload can adopt a run
            // that was already in flight. Whether an event belongs to
            // the run on screen is `lastEndedRunID`'s question; the
            // phase this client left behind answers nothing about a run
            // it never started, and treating it as a gate dropped the
            // whole turn.
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt && event.runID !== context.lastEndedRunID,
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: () => '',
              }),
            },
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                (event.runID !== context.currentRunID ||
                  event.stage !== context.turnStage),
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: ({ event }) => event.stage ?? '',
              }),
            },
            TURN_ENDED: [
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.lastEndedRunID &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: ({ event }) =>
                    event.status === 'failed' ||
                    event.status === 'aborted' ||
                    event.status === 'canceled' ||
                    event.status === 'interrupted'
                      ? event.status
                      : undefined,
                  failureErrorKind: ({ event }) => event.errorKind,
                  turnError: ({ event }) => event.error,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt && event.runID !== context.lastEndedRunID,
                actions: assign({
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
            ],
          },
        },
        failed: {
          on: {
            SEND_STARTED: {
              guard: ({ context }) => !context.deletedAt,
              target: 'starting',
              actions: assign({
                supersededRunID: () => undefined,
              }),
            },
            DISMISS_FAILURE: {
              guard: ({ context }) => !context.deletedAt,
              target: 'idle',
            },
            // Same outs as `succeeded`: a failure banner is not a reason
            // to drop the next run's start, deltas or terminal event.
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt && event.runID !== context.lastEndedRunID,
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: () => '',
                failureStatus: () => undefined,
                failureErrorKind: () => undefined,
                turnError: () => undefined,
              }),
            },
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                event.runID !== context.lastEndedRunID &&
                (event.runID !== context.currentRunID ||
                  event.stage !== context.turnStage),
              target: 'running',
              actions: assign({
                currentRunID: ({ event }) => event.runID,
                supersededRunID: () => undefined,
                turnStage: ({ event }) => event.stage ?? '',
                failureStatus: () => undefined,
                failureErrorKind: () => undefined,
                turnError: () => undefined,
              }),
            },
            TURN_ENDED: [
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.lastEndedRunID &&
                  event.status !== 'completed',
                actions: assign({
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: ({ event }) =>
                    event.status === 'failed' ||
                    event.status === 'aborted' ||
                    event.status === 'canceled' ||
                    event.status === 'interrupted'
                      ? event.status
                      : undefined,
                  failureErrorKind: ({ event }) => event.errorKind,
                  turnError: ({ event }) => event.error,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt && event.runID !== context.lastEndedRunID,
                target: 'idle',
                actions: assign({
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
            ],
          },
        },
      },
    },
  },
});

export function createConversationActor(input: ConversationInput) {
  const actor = createActor(conversationMachine, { input });
  actor.start();
  if (input.readyEmpty) {
    actor.send({ type: 'NEW_CHAT_READY' });
  }
  return actor;
}
