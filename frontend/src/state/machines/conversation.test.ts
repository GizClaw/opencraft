import { describe, expect, it } from 'vitest';
import { createActor } from 'xstate';
import { conversationMachine, submissionEffect } from './conversation';

const start = (readyEmpty = false) => {
  const actor = createActor(conversationMachine, {
    input: {
      conversationID: 's-1',
      workspaceGeneration: 1,
      readyEmpty,
    },
  });
  actor.start();
  return actor;
};

const regions = (actor: ReturnType<typeof start>) =>
  actor.getSnapshot().value as {
    lifecycle: string;
    transcript: string;
    turn: string;
  };

const endTurn = (
  actor: ReturnType<typeof start>,
  runID: string,
  status: 'completed' | 'failed' | 'canceled' | 'interrupted' | 'aborted',
) =>
  actor.send({
    type: 'TURN_ENDED',
    runID,
    status,
  });

describe('conversation machine', () => {
  it('starts with independent orthogonal regions', () => {
    const actor = start();
    expect(actor.getSnapshot().value).toEqual({
      lifecycle: 'alive',
      transcript: 'unloaded',
      turn: 'idle',
    });
  });

  it('a new chat skips hydration and becomes ready(empty)', () => {
    const actor = start();
    actor.send({ type: 'NEW_CHAT_READY' });
    expect(actor.getSnapshot().value).toEqual({
      lifecycle: 'alive',
      transcript: 'ready',
      turn: 'idle',
    });
  });

  it('hydrates with request and generation guards', () => {
    const actor = start();
    actor.send({ type: 'HYDRATE_REQUESTED', request: 1, generation: 1 });
    // Stale request: keep loading.
    actor.send({
      type: 'HYDRATE_OK',
      request: 0,
      generation: 1,
      empty: true,
    });
    expect(regions(actor).transcript).toBe('loading');
    // Stale generation: keep loading.
    actor.send({
      type: 'HYDRATE_OK',
      request: 1,
      generation: 2,
      empty: true,
    });
    expect(regions(actor).transcript).toBe('loading');
    // Current request wins.
    actor.send({
      type: 'HYDRATE_OK',
      request: 1,
      generation: 1,
      empty: true,
    });
    expect(regions(actor).transcript).toBe('ready');
  });

  it('transcript failure can coexist with a running turn', () => {
    const actor = start();
    actor.send({ type: 'HYDRATE_REQUESTED', request: 1, generation: 1 });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    expect(actor.getSnapshot().value).toEqual({
      lifecycle: 'alive',
      transcript: 'loading',
      turn: 'running',
    });
    actor.send({
      type: 'HYDRATE_FAIL',
      request: 1,
      generation: 1,
      error: 'archive missing',
    });
    expect(regions(actor).transcript).toBe('failed');
    expect(regions(actor).turn).toBe('running');
  });

  it('runs a complete turn through failed and dismissal', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor.send({ type: 'STREAM', runID: 'r-1', stage: 'text' });
    expect(regions(actor).turn).toBe('running');

    endTurn(actor, 'r-1', 'canceled');
    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      failureStatus: 'canceled',
    });

    actor.send({ type: 'DISMISS_FAILURE' });
    expect(regions(actor).turn).toBe('idle');
  });

  it('ignores a late turn_end for an already-finished run', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'completed');
    expect(regions(actor).turn).toBe('succeeded');

    endTurn(actor, 'r-1', 'failed');
    expect(regions(actor).turn).toBe('succeeded');
  });

  it('keeps the error kind a deadline carries alongside the cancel', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor.send({
      type: 'TURN_ENDED',
      runID: 'r-1',
      status: 'canceled',
      error: 'context deadline exceeded',
      errorKind: 'timeout',
    });

    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      failureStatus: 'canceled',
      failureErrorKind: 'timeout',
      turnError: 'context deadline exceeded',
    });
  });

  it('a barge-in send supersedes the old run while it is starting', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'STREAM', runID: 'r-old', stage: 'text' });
    expect(regions(actor).turn).toBe('running');

    // The replacement send moves back to starting and marks the old
    // run as superseded.
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    expect(regions(actor).turn).toBe('starting');
    expect(actor.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
    });

    // Late deltas and the terminal event of the superseded run must
    // not disturb the starting state.
    actor.send({ type: 'STREAM', runID: 'r-old', stage: 'tool:x' });
    endTurn(actor, 'r-old', 'interrupted');
    expect(regions(actor).turn).toBe('starting');
    expect(actor.getSnapshot().context).not.toMatchObject({
      failureStatus: 'interrupted',
    });

    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
      supersededRunID: undefined,
    });
  });

  it('accepts the replacement run before the superseded turn ends', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });

    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
    });

    // A terminal event for the old run is inert once the replacement
    // owns the running state.
    endTurn(actor, 'r-old', 'interrupted');
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
    });
  });

  // The replaced run is over, and the memory has to say so even though
  // the run never becomes the last one to end: the replacement's ending
  // is. One slot could not hold both, and a late delta of the replaced
  // run then read as a brand-new live run - the conversation was pinned
  // to a run whose terminal event it had already consumed.
  it('remembers the replaced run once the replacement starts', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    // The engine finalizes the old run before the replacement starts,
    // so this is the shape the barge wait actually sees.
    endTurn(actor, 'r-old', 'interrupted');

    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });
    endTurn(actor, 'r-new', 'completed');
    expect(regions(actor).turn).toBe('succeeded');
    expect(actor.getSnapshot().context.endedRunIDs).toEqual(['r-old', 'r-new']);

    // The stale delta of the replaced run stays inert: whichever phase
    // the turn is in, that run is over.
    actor.send({ type: 'STREAM', runID: 'r-old', stage: 'text' });
    expect(regions(actor).turn).toBe('succeeded');
  });

  it('remembers a replaced run whose ending arrives after the replacement started', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    // The replacement wins the race; the replaced run's terminal event
    // then lands with nothing watching it.
    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });

    endTurn(actor, 'r-old', 'interrupted');
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
      endedRunIDs: ['r-old'],
    });

    endTurn(actor, 'r-new', 'completed');
    expect(regions(actor).turn).toBe('succeeded');
    actor.send({ type: 'STREAM', runID: 'r-old', stage: 'text' });
    expect(regions(actor).turn).toBe('succeeded');
  });

  it('a failed plain send keeps the ended-run memory', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'completed');
    expect(regions(actor).turn).toBe('succeeded');

    // The next send fails before it produced a run: there is no run to
    // report, and what did end has to stay remembered. A synthetic
    // terminal event with an empty run id used to wipe the slot here.
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'START_FAILED', error: 'start boom' });
    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      endedRunIDs: ['r-1'],
      failureStatus: 'failed',
      turnError: 'start boom',
    });

    // Both stale shapes of r-1 stay inert in the failed turn.
    actor.send({ type: 'STREAM', runID: 'r-1', stage: 'text' });
    endTurn(actor, 'r-1', 'failed');
    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context.failureStatus).toBe('failed');
  });

  it('a force-canceled superseded run still waits for the replacement', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    expect(regions(actor).turn).toBe('starting');

    // Stop during the barge wait hard-cancels the superseded run; its
    // canceled terminal event must not settle the conversation while
    // the replacement is still starting.
    endTurn(actor, 'r-old', 'canceled');
    expect(regions(actor).turn).toBe('starting');
    expect(actor.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
      currentRunID: undefined,
    });
    expect(actor.getSnapshot().context).not.toMatchObject({
      failureStatus: 'canceled',
    });

    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
      supersededRunID: undefined,
    });
  });

  it('a failed barge-in resumes the superseded run when it is alive', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    expect(actor.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
    });

    actor.send({ type: 'START_FAILED', error: 'start boom' });
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-old',
      supersededRunID: undefined,
      failureStatus: undefined,
    });

    // The restored run's own terminal event still drives the state.
    endTurn(actor, 'r-old', 'completed');
    expect(regions(actor).turn).toBe('succeeded');
  });

  // Whether the superseded run is still alive is a question its own
  // events answer, and this machine is what sees them: the terminal
  // event that lands during the barge wait is recorded in `starting`,
  // and only the failed start acts on it.
  it('records the superseded run\u2019s ending while the replacement starts', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });

    actor.send({
      type: 'TURN_ENDED',
      runID: 'r-old',
      status: 'interrupted',
      error: 'engine boom',
    });

    // Recorded, not acted on: the replacement is still starting and
    // owns the turn.
    expect(regions(actor).turn).toBe('starting');
    expect(actor.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
      supersededEnd: { status: 'interrupted', error: 'engine boom' },
    });
    expect(actor.getSnapshot().context).not.toMatchObject({
      failureStatus: 'interrupted',
    });

    actor.send({ type: 'START_FAILED', error: 'start boom' });
    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: undefined,
      supersededRunID: undefined,
      supersededEnd: undefined,
      endedRunIDs: ['r-old'],
      failureStatus: 'interrupted',
      turnError: 'engine boom',
    });
  });

  it('a failed barge-in absorbs a completed superseded run', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });

    endTurn(actor, 'r-old', 'completed');
    actor.send({ type: 'START_FAILED', error: 'start boom' });

    expect(regions(actor).turn).toBe('succeeded');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: undefined,
      supersededRunID: undefined,
      supersededEnd: undefined,
      endedRunIDs: ['r-old'],
    });
  });

  it('clears the recorded ending when the replacement does start', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    endTurn(actor, 'r-old', 'canceled');

    actor.send({ type: 'RUN_STARTED', runID: 'r-new' });

    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
      supersededRunID: undefined,
      supersededEnd: undefined,
    });
    // The canceled ending of the old run never became this turn's
    // failure.
    expect(actor.getSnapshot().context.failureStatus).toBeUndefined();
  });

  it('restores an idle conversation from the first stream after reload', () => {
    const actor = start();
    actor.send({ type: 'STREAM', runID: 'r-1', stage: 'text' });
    expect(regions(actor).turn).toBe('running');
  });

  it('deleted conversations ignore late events', () => {
    const actor = start();
    actor.send({ type: 'SESSION_DELETED', deletedAt: 'now' });
    actor.send({ type: 'STREAM', runID: 'r-late' });
    actor.send({ type: 'HYDRATE_REQUESTED', request: 1, generation: 1 });
    actor.send({ type: 'SUBMIT', kind: 'send' });
    expect(actor.getSnapshot().value).toEqual({
      lifecycle: 'deleted',
      transcript: 'unloaded',
      turn: 'idle',
    });
  });
});

// A run this client did not start — a scheduled automation pinned to the
// conversation, or a live run a reload adopted — begins and ends with no
// send of ours behind it. A resting turn is not a gate for those events;
// the run's identity is, and the ended-run memory is what says an event is
// too late. Treating the phase as the gate dropped such a run whole.

describe('the submission table', () => {
  // The table is small enough to pin whole. The store asks it before it
  // spends an IPC; the guards above read the same cells, so every phase
  // and kind pair is worth one line here.
  it('answers the phase the turn region is in', () => {
    const view = (turn: string) => ({ value: { turn }, context: {} });

    expect(submissionEffect(view('idle'), 'send')).toBe('start');
    expect(submissionEffect(view('succeeded'), 'send')).toBe('start');
    expect(submissionEffect(view('failed'), 'send')).toBe('start');
    expect(submissionEffect(view('starting'), 'send')).toBe('refuse');
    expect(submissionEffect(view('running'), 'send')).toBe('refuse');

    expect(submissionEffect(view('starting'), 'interrupt')).toBe('stage');
    expect(submissionEffect(view('running'), 'interrupt')).toBe('barge');
    expect(submissionEffect(view('running'), 'drain')).toBe('barge');
    expect(submissionEffect(view('starting'), 'drain')).toBe('refuse');

    expect(submissionEffect(view('running'), 'steer')).toBe('steer');
    expect(submissionEffect(view('idle'), 'steer')).toBe('refuse');
    expect(submissionEffect(view('running'), 'queue')).toBe('stage');
    expect(submissionEffect(view('idle'), 'queue')).toBe('refuse');
    expect(submissionEffect(view('running'), 'resend')).toBe('stage');
    expect(submissionEffect(view('failed'), 'resend')).toBe('start');

    // A conversation without an actor has no turn yet: the initial one.
    expect(submissionEffect(undefined, 'send')).toBe('start');
    expect(submissionEffect(undefined, 'queue')).toBe('refuse');
  });

  it('refuses everything for a deleted conversation', () => {
    const deleted = {
      value: { turn: 'running' },
      context: { deletedAt: 'now' },
    };
    const kinds = [
      'send',
      'interrupt',
      'steer',
      'queue',
      'resend',
      'drain',
    ] as const;
    for (const kind of kinds) {
      expect(submissionEffect(deleted, kind)).toBe('refuse');
    }
  });

  it('a reply cannot take a live run\u2019s turn', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });

    actor.send({ type: 'SUBMIT', kind: 'send' });

    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-1',
      supersededRunID: undefined,
    });
  });

  it('an interrupt waits while the awaited run is still starting', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });

    // The 'stage' cell is the store's to act on (it parks the draft);
    // the machine itself stays where it is.
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });

    expect(regions(actor).turn).toBe('starting');
    expect(actor.getSnapshot().context.supersededRunID).toBeUndefined();
  });

  it('a queue and a steer never move the turn', () => {
    const phases: Array<[string, () => ReturnType<typeof start>]> = [
      ['idle', () => start()],
      [
        'starting',
        () => {
          const actor = start();
          actor.send({ type: 'SUBMIT', kind: 'send' });
          return actor;
        },
      ],
      [
        'running',
        () => {
          const actor = start();
          actor.send({ type: 'SUBMIT', kind: 'send' });
          actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
          return actor;
        },
      ],
    ];

    for (const [phase, make] of phases) {
      const actor = make();
      const before = actor.getSnapshot().value;
      for (const kind of ['queue', 'steer'] as const) {
        actor.send({ type: 'SUBMIT', kind });
      }
      expect(actor.getSnapshot().value).toEqual(before);
      expect(regions(actor).turn).toBe(phase);
    }
  });

  it('a resend starts a quiet turn and waits while one is live', () => {
    const quiet = start();
    quiet.send({ type: 'SUBMIT', kind: 'resend' });
    expect(regions(quiet).turn).toBe('starting');

    const live = start();
    live.send({ type: 'SUBMIT', kind: 'send' });
    live.send({ type: 'RUN_STARTED', runID: 'r-1' });
    live.send({ type: 'SUBMIT', kind: 'resend' });
    expect(regions(live).turn).toBe('running');
    expect(live.getSnapshot().context.currentRunID).toBe('r-1');
  });

  it('a deleted conversation refuses every submission', () => {
    const actor = start();
    actor.send({ type: 'SESSION_DELETED', deletedAt: 'now' });

    for (const kind of [
      'send',
      'interrupt',
      'steer',
      'queue',
      'resend',
      'drain',
    ] as const) {
      actor.send({ type: 'SUBMIT', kind });
    }

    expect(regions(actor).turn).toBe('idle');
  });
});

describe('runs that started outside this client', () => {
  const finishedTurn = () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'completed');
    expect(regions(actor).turn).toBe('succeeded');
    return actor;
  };

  it('takes the run start of an automation scheduled on a finished turn', () => {
    const actor = finishedTurn();

    actor.send({ type: 'RUN_STARTED', runID: 'r-2' });
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-2',
      endedRunIDs: ['r-1'],
    });

    actor.send({ type: 'STREAM', runID: 'r-2', stage: 'text' });
    expect(actor.getSnapshot().context).toMatchObject({ turnStage: 'text' });

    endTurn(actor, 'r-2', 'failed');
    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      endedRunIDs: ['r-1', 'r-2'],
      failureStatus: 'failed',
    });
  });

  it('takes the first delta of a foreign run after a failure', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'canceled');
    expect(regions(actor).turn).toBe('failed');

    // No run-start ever reaches this client when the stream is the only
    // event the run produced — the delta itself is the proof it is live.
    actor.send({ type: 'STREAM', runID: 'r-2', stage: 'reasoning' });
    expect(regions(actor).turn).toBe('running');
    expect(actor.getSnapshot().context).toMatchObject({
      currentRunID: 'r-2',
      turnStage: 'reasoning',
      failureStatus: undefined,
    });
  });

  it('records a terminal event for a run it never watched', () => {
    // The reload case: the run was already in flight when the window came
    // back, and it ends without producing a delta the new actor saw.
    const actor = start();
    actor.send({
      type: 'TURN_ENDED',
      runID: 'r-9',
      status: 'canceled',
      error: 'context deadline exceeded',
      errorKind: 'timeout',
    });

    expect(regions(actor).turn).toBe('failed');
    expect(actor.getSnapshot().context).toMatchObject({
      endedRunIDs: ['r-9'],
      failureStatus: 'canceled',
      failureErrorKind: 'timeout',
      turnError: 'context deadline exceeded',
    });
  });

  it('still ignores anything from a run that has already ended', () => {
    const actor = finishedTurn();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-2' });
    endTurn(actor, 'r-2', 'completed');
    expect(regions(actor).turn).toBe('succeeded');

    // Both the duplicate terminal event and a late delta of r-2 stay
    // inert: the ended-run memory is what says so.
    endTurn(actor, 'r-2', 'failed');
    actor.send({ type: 'STREAM', runID: 'r-2', stage: 'text' });
    expect(regions(actor).turn).toBe('succeeded');
    expect(actor.getSnapshot().context).toMatchObject({
      endedRunIDs: ['r-1', 'r-2'],
      failureStatus: undefined,
    });
  });

  it('ignores a replayed start once a failed turn is dismissed', () => {
    const actor = start();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'canceled');
    expect(regions(actor).turn).toBe('failed');
    actor.send({ type: 'DISMISS_FAILURE' });
    expect(regions(actor).turn).toBe('idle');

    // The router's gate drops a start like this one today; the idle
    // column is the last of the five that says so on its own.
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    expect(regions(actor).turn).toBe('idle');
    expect(actor.getSnapshot().context.currentRunID).toBeUndefined();
  });
});
