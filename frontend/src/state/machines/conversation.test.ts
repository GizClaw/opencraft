import { describe, expect, it } from 'vitest';
import { createActor } from 'xstate';
import { conversationMachine } from './conversation';

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
    actor.send({ type: 'SEND_STARTED' });
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
    actor.send({ type: 'SEND_STARTED' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    endTurn(actor, 'r-1', 'completed');
    expect(regions(actor).turn).toBe('succeeded');

    endTurn(actor, 'r-1', 'failed');
    expect(regions(actor).turn).toBe('succeeded');
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
    actor.send({ type: 'SEND_STARTED' });
    expect(actor.getSnapshot().value).toEqual({
      lifecycle: 'deleted',
      transcript: 'unloaded',
      turn: 'idle',
    });
  });
});
