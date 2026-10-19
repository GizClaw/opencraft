import { assign, createActor, createMachine } from 'xstate';

export interface ConversationInput {
  conversationID: string;
  workspaceGeneration?: number;
  readyEmpty?: boolean;
  workspace?: string;
}

// The submission table
// ---------------------------------------------------------------------
// Every way the turn is handed something new - a reply, an "answer me
// now", a steer, a Tab draft - plus the drain that fires a staged draft,
// is answered from this one table. The store asks `submissionEffect`
// before it spends an IPC or draws a row; the machine's own guards ask
// the same cells (`submitting`), so a submission the table refuses in
// the current phase cannot move the machine either.
//
// The phases are the turn region collapsed to the three shapes a
// submission can meet:
//
//   quiescent  idle / succeeded / failed: no run is being watched
//   starting   a send is in flight (possibly over a run: see
//              supersededRunID), so the turn is spoken for but has no
//              run of its own yet
//   running    a run is live and streaming
//
// and the effects are what the answer means for the asker:
//
//   start   begin a turn (the awaited run, if any, is superseded)
//   barge   begin a turn *over* the live run, which is superseded
//   stage   park the input until the awaited run starts or ends
//   steer   hand the text to the live run itself
//   refuse  this submission has nothing to act on here
//
// A deleted conversation refuses everything: the machine's guards all
// carry that flag, so the table carries it too.
export type TurnPhase = 'quiescent' | 'starting' | 'running';
export type SubmitKind =
  'send' | 'interrupt' | 'steer' | 'queue' | 'resend' | 'drain';
export type SubmitEffect = 'start' | 'barge' | 'stage' | 'steer' | 'refuse';

const SUBMISSIONS: Record<SubmitKind, Record<TurnPhase, SubmitEffect>> = {
  // A reply: only a quiet turn takes one.
  send: { quiescent: 'start', starting: 'refuse', running: 'refuse' },
  // "Answer me now": over a live run it barges in; while the awaited
  // run is still starting there is nothing to interrupt yet, so the
  // input waits for it.
  interrupt: { quiescent: 'start', starting: 'stage', running: 'barge' },
  // A mid-turn message goes into the run itself, and nowhere else.
  steer: { quiescent: 'refuse', starting: 'refuse', running: 'steer' },
  // A Tab draft waits for the turn it was written under, always.
  queue: { quiescent: 'refuse', starting: 'stage', running: 'stage' },
  // Re-sending the text of an undelivered interjection: the run it was
  // written under is over, so a quiet turn takes it as a turn of its
  // own and a live one stages it the way a Tab draft would.
  resend: { quiescent: 'start', starting: 'stage', running: 'stage' },
  // The drain: a staged draft taking over the turn of the run it waited
  // for. That run's terminal event is still in flight when the draft
  // fires (the data layer runs before the actor sees the event), so the
  // draft takes the barge shape - taking the run out of the current
  // slot is what keeps its ending inert.
  drain: { quiescent: 'start', starting: 'refuse', running: 'barge' },
};

// TurnView is what the table is read from: the turn region's value and
// the one context flag it is gated on. Callers hold snapshots loosely
// typed, so this names the fields it needs instead of the snapshot type.
export interface TurnView {
  value: unknown;
  context: { deletedAt?: string };
}

function turnPhase(value: unknown): TurnPhase {
  const turn = (value as { turn?: string } | undefined)?.turn;
  return turn === 'starting' || turn === 'running' ? turn : 'quiescent';
}

export function submissionEffect(
  view: TurnView | undefined,
  kind: SubmitKind,
): SubmitEffect {
  // A conversation without an actor has no turn yet: read the table in
  // the phase it would be in, which is the initial one.
  if (!view) return SUBMISSIONS[kind].quiescent;
  if (view.context.deletedAt) return 'refuse';
  return SUBMISSIONS[kind][turnPhase(view.value)];
}

// submitting is the same table read from a guard, with the node's own
// phase written out at the edit site: `submitting(context, kind,
// 'running', 'barge')` is the transition that implements that cell, so
// changing the cell changes the guard with it.
function submitting(
  context: { deletedAt?: string },
  kind: SubmitKind,
  phase: TurnPhase,
  effect: SubmitEffect,
): boolean {
  return !context.deletedAt && SUBMISSIONS[kind][phase] === effect;
}

// How many ended runs a conversation remembers. An event that names a
// run whose terminal event this machine has already seen is stale by
// definition, and one slot cannot say that: a barge-in replaces a run
// without that run ever becoming the last one to end, and its late
// deltas must not be able to slip in behind the replacement's own
// ending. Bounded because the memory only has to outlive the staleness
// window, not the conversation: a re-created actor (a reload, an evicted
// background conversation, a workspace reset) knows nothing about a run
// it never saw, which is the same trade the gate already makes for runs
// this client did not start.
const ENDED_RUN_MEMORY = 32;

/** Whether this run's terminal event has already been seen. */
export function isEndedRun(
  context: { endedRunIDs?: string[] },
  runID: string,
): boolean {
  return context.endedRunIDs?.includes(runID) ?? false;
}

function rememberEnded(
  context: { endedRunIDs?: string[] },
  runID: string,
): string[] {
  const ended = context.endedRunIDs ?? [];
  // An empty id is no run at all; the memory only keeps runs that
  // ended. (A failed send used to synthesize a terminal event with one.)
  if (!runID || ended.includes(runID)) return ended;
  return [...ended, runID].slice(-ENDED_RUN_MEMORY);
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
  // The runs whose terminal event this machine has already seen, most
  // recent last (bounded, see ENDED_RUN_MEMORY). Whatever a run says
  // after its ending - a late delta, a replayed terminal - is stale,
  // and this is what says so.
  endedRunIDs?: string[];
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
  // BIND_WORKSPACE is the backend naming the workspace that owns this
  // conversation, which is not a turn event: the root `on` below accepts
  // it in every phase.
  | { type: 'BIND_WORKSPACE'; workspace: string }
  // SUBMIT carries a submission into the turn region; the table above
  // says what it does in the phase the turn is in.
  | { type: 'SUBMIT'; kind: SubmitKind }
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
  // Which workspace owns this conversation is control-plane metadata
  // about the run, not about the turn, so every phase accepts the
  // rebind: the first event of a run started in a workspace this client
  // has not touched is often the only thing that says where its row
  // belongs, and it can arrive in any phase.
  on: {
    BIND_WORKSPACE: {
      actions: assign({
        workspace: ({ event }) => event.workspace,
      }),
    },
  },
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
            // The quiescent column: a reply or an "answer me now"
            // begins the turn here.
            SUBMIT: {
              guard: ({ context, event }) =>
                submitting(context, event.kind, 'quiescent', 'start'),
              target: 'starting',
            },
            // ActiveRun() recovery: a conversation actor created after
            // the run already started receives RUN_STARTED directly.
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt && !isEndedRun(context, event.runID),
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
            // ignored: the ended-run memory says that run is over.
            STREAM: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                !isEndedRun(context, event.runID) &&
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
                  !isEndedRun(context, event.runID) &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
                  !context.deletedAt && !isEndedRun(context, event.runID),
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
            // Nothing starts a turn in this column: the table refuses a
            // reply and a steer here, stages an "answer me now" and a
            // Tab draft (the store parks them), and never runs a drain
            // while a send is in flight - so there is no SUBMIT
            // transition to write.
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
                  endedRunIDs: ({ context }) =>
                    rememberEnded(context, context.supersededRunID ?? ''),
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
                  endedRunIDs: ({ context }) =>
                    rememberEnded(context, context.supersededRunID ?? ''),
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
              {
                // A plain send failed before any run existed: there is
                // no run to resume or to absorb, so the failure is the
                // whole fact and nothing about an ended run changes.
                guard: ({ context }) =>
                  !context.deletedAt && context.supersededRunID === undefined,
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededEnd: () => undefined,
                  failureStatus: () => 'failed',
                  failureErrorKind: () => undefined,
                  turnError: ({ event }) => event.error,
                }),
              },
            ],
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt &&
                !isEndedRun(context, event.runID) &&
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
                !isEndedRun(context, event.runID) &&
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
                // stay in `starting`: a failed start absorbs it (see
                // START_FAILED), a replacement that does start drops
                // `supersededEnd` with the run swap, and the ended-run
                // memory keeps the run's late events inert either way.
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
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
                }),
              },
              {
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.supersededRunID &&
                  event.status !== 'completed' &&
                  !isEndedRun(context, event.runID),
                target: 'failed',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
                  !context.deletedAt &&
                  event.runID !== context.supersededRunID &&
                  !isEndedRun(context, event.runID),
                target: 'succeeded',
                actions: assign({
                  currentRunID: () => undefined,
                  supersededRunID: () => undefined,
                  supersededEnd: () => undefined,
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
            // The running column: only a submission whose cell is
            // 'barge' may take the turn away from a live run - an
            // "answer me now", or the drain of a run that just ended.
            // The live run is superseded here: its deltas and terminal
            // event stay inert while the replacement starts (see
            // `starting`), and the engine interrupts it and only starts
            // the replacement after it has been finalized.
            SUBMIT: {
              guard: ({ context, event }) =>
                submitting(context, event.kind, 'running', 'barge'),
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
                !isEndedRun(context, event.runID) &&
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
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
                  failureStatus: () => undefined,
                  failureErrorKind: () => undefined,
                  turnError: () => undefined,
                }),
              },
              {
                // A terminal event for a run this conversation is not
                // watching right now: the run a barge-in replaced, or
                // one whose ending raced the replacement's start. The
                // run is over - remember it so its late deltas and any
                // replay of this event can never touch the transcript
                // again, and stay on the run that is live.
                guard: ({ context, event }) =>
                  !context.deletedAt &&
                  event.runID !== context.currentRunID &&
                  event.runID !== context.supersededRunID &&
                  !isEndedRun(context, event.runID),
                actions: assign({
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
                }),
              },
            ],
          },
        },
        succeeded: {
          on: {
            // The quiescent column again: the turn is over, so a reply
            // or an "answer me now" begins the next one.
            SUBMIT: {
              guard: ({ context, event }) =>
                submitting(context, event.kind, 'quiescent', 'start'),
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
            // a run that is still live is the ended-run memory's
            // question; the phase this client left behind answers
            // nothing about a run it never started, and treating it as
            // a gate dropped the whole turn.
            RUN_STARTED: {
              guard: ({ context, event }) =>
                !context.deletedAt && !isEndedRun(context, event.runID),
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
                !isEndedRun(context, event.runID) &&
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
                  !isEndedRun(context, event.runID) &&
                  event.status !== 'completed',
                target: 'failed',
                actions: assign({
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
                  !context.deletedAt && !isEndedRun(context, event.runID),
                actions: assign({
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
            SUBMIT: {
              guard: ({ context, event }) =>
                submitting(context, event.kind, 'quiescent', 'start'),
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
                !context.deletedAt && !isEndedRun(context, event.runID),
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
                !isEndedRun(context, event.runID) &&
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
                  !isEndedRun(context, event.runID) &&
                  event.status !== 'completed',
                actions: assign({
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
                  !context.deletedAt && !isEndedRun(context, event.runID),
                target: 'idle',
                actions: assign({
                  endedRunIDs: ({ context, event }) =>
                    rememberEnded(context, event.runID),
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
