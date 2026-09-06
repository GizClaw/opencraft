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
  lastEndedRunID?: string;
  turnStage?: string;
  failureStatus?: 'failed' | 'aborted' | 'canceled' | 'interrupted';
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
  | {
      type: 'START_FAILED';
      error: string;
      // supersededEndedStatus is set when the superseded run's
      // terminal event was already consumed while the barge-in was
      // still starting. Its absence means the old run is still live.
      supersededEndedStatus?: TurnEndStatus;
      supersededEndedError?: string;
    }
  | { type: 'RUN_STARTED'; runID: string }
  | { type: 'STREAM'; runID: string; stage?: string }
  | {
      type: 'TURN_ENDED';
      runID: string;
      status: TurnEndStatus;
      error?: string;
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
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  context.supersededRunID !== undefined &&
                  event.supersededEndedStatus === undefined,
                target: 'running',
                actions: assign({
                  currentRunID: ({ context }) => context.supersededRunID,
                  supersededRunID: () => undefined,
                  turnStage: () => '',
                  failureStatus: () => undefined,
                  turnError: () => undefined,
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  context.supersededRunID !== undefined &&
                  event.supersededEndedStatus === 'completed',
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  lastEndedRunID: ({ context }) => context.supersededRunID,
                  failureStatus: () => undefined,
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
                  lastEndedRunID: ({ context }) => context.supersededRunID,
                  failureStatus: ({ event }) =>
                    event.supersededEndedStatus === 'failed' ||
                    event.supersededEndedStatus === 'aborted' ||
                    event.supersededEndedStatus === 'canceled' ||
                    event.supersededEndedStatus === 'interrupted'
                      ? event.supersededEndedStatus
                      : 'failed',
                  turnError: ({ event }) =>
                    event.supersededEndedError ?? event.error,
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
                turnStage: ({ event }) => event.stage ?? '',
              }),
            },
            TURN_ENDED: [
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.supersededRunID &&
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
                  lastEndedRunID: ({ event }) => event.runID,
                  failureStatus: () => undefined,
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
