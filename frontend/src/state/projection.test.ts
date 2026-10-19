// state/projection.ts: the one place a machine snapshot becomes the
// union the UI reads. Cases drive the real machines wherever the machine
// can produce the shape; a hand-built snapshot covers only the fallbacks
// an unknown state name lands on, which no machine state reaches.

import { createActor } from 'xstate';
import { describe, expect, it } from 'vitest';
import {
  createConversationActor,
  type ConversationInput,
} from './machines/conversation';
import { sessionFocusMachine } from './machines/sessionFocus';
import { projectConversation, projectFocus } from './projection';

type ConversationSnapshot = Parameters<typeof projectConversation>[0];
type FocusSnapshot = Parameters<typeof projectFocus>[0];

function conversation(input: Partial<ConversationInput> = {}) {
  const actor = createConversationActor({ conversationID: 's-1', ...input });
  return {
    actor,
    project: () =>
      projectConversation(
        actor.getSnapshot() as unknown as ConversationSnapshot,
      ),
  };
}

function focus(sessionID = '') {
  const actor = createActor(sessionFocusMachine, { input: { sessionID } });
  actor.start();
  return {
    actor,
    project: () =>
      projectFocus(actor.getSnapshot() as unknown as FocusSnapshot),
  };
}

describe('projectConversation', () => {
  it('reports a fresh actor as alive, unloaded and idle', () => {
    expect(conversation().project()).toEqual({
      lifecycle: { name: 'alive' },
      transcript: { name: 'unloaded' },
      turn: { name: 'idle' },
    });
  });

  it('carries the empty flag a ready transcript was opened with', () => {
    expect(conversation({ readyEmpty: true }).project().transcript).toEqual({
      name: 'ready',
      empty: true,
    });
  });

  it('walks a hydrate from loading to ready', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'HYDRATE_REQUESTED', request: 1, generation: 0 });
    expect(project().transcript).toEqual({ name: 'loading' });

    actor.send({ type: 'HYDRATE_OK', request: 1, generation: 0, empty: false });
    expect(project().transcript).toEqual({ name: 'ready', empty: false });
  });

  it('reports a hydrate that failed with the error it carried', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'HYDRATE_REQUESTED', request: 1, generation: 0 });
    actor.send({
      type: 'HYDRATE_FAIL',
      request: 1,
      generation: 0,
      error: 'read failed',
    });
    expect(project().transcript).toEqual({
      name: 'failed',
      error: 'read failed',
    });
  });

  it('reports the run a starting turn is replacing', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'SUBMIT', kind: 'send' });
    expect(project().turn).toEqual({ name: 'starting' });

    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor.send({ type: 'SUBMIT', kind: 'interrupt' });
    expect(project().turn).toEqual({
      name: 'starting',
      supersededRunID: 'r-1',
    });
  });

  it('keeps the run id while its stage moves', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    expect(project().turn).toEqual({
      name: 'running',
      runID: 'r-1',
      stage: '',
    });

    actor.send({ type: 'STREAM', runID: 'r-1', stage: 'thinking' });
    expect(project().turn).toEqual({
      name: 'running',
      runID: 'r-1',
      stage: 'thinking',
    });
  });

  it('projects a completed turn as succeeded', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });

    expect(project().turn).toEqual({ name: 'succeeded' });
  });

  it('keeps the status, error and error kind of a failed turn', () => {
    const { actor, project } = conversation();

    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor.send({
      type: 'TURN_ENDED',
      runID: 'r-1',
      status: 'canceled',
      error: 'deadline exceeded',
      errorKind: 'timeout',
    });

    // Status alone cannot tell a deadline from a user stop; the kind is
    // what the failure text is chosen from.
    expect(project().turn).toEqual({
      name: 'failed',
      status: 'canceled',
      error: 'deadline exceeded',
      errorKind: 'timeout',
    });
  });

  it('projects the lifecycle tombstone next to whatever the turn was', () => {
    const { actor, project } = conversation();
    actor.send({ type: 'SUBMIT', kind: 'send' });
    actor.send({ type: 'RUN_STARTED', runID: 'r-1' });

    actor.send({ type: 'SESSION_DELETED' });

    expect(project()).toMatchObject({
      lifecycle: { name: 'deleted' },
      turn: { name: 'running', runID: 'r-1' },
    });
  });

  it('falls back to unloaded, idle and an explicit error for unknown shapes', () => {
    const snapshot = (
      value: { lifecycle: string; transcript: string; turn: string },
      context: ConversationSnapshot['context'] = {},
    ) =>
      projectConversation({
        value,
        context,
      });

    expect(
      snapshot({ lifecycle: 'alive', transcript: 'draft', turn: 'paused' }),
    ).toEqual({
      lifecycle: { name: 'alive' },
      transcript: { name: 'unloaded' },
      turn: { name: 'idle' },
    });

    // A failure without its payload still has to say something: the UI
    // prints the string, so it cannot be empty.
    expect(
      snapshot({ lifecycle: 'zombie', transcript: 'failed', turn: 'failed' }),
    ).toEqual({
      lifecycle: { name: 'alive' },
      transcript: { name: 'failed', error: 'unknown error' },
      turn: {
        name: 'failed',
        status: 'failed',
        error: undefined,
        errorKind: undefined,
      },
    });

    expect(
      snapshot({ lifecycle: 'alive', transcript: 'ready', turn: 'running' }),
    ).toEqual({
      lifecycle: { name: 'alive' },
      transcript: { name: 'ready', empty: false },
      turn: { name: 'running', runID: '', stage: '' },
    });
  });
});

describe('projectFocus', () => {
  it('starts with no session', () => {
    expect(focus().project()).toEqual({ name: 'no-session' });
  });

  it('opens an existing session with the request that asked for it', () => {
    const { actor, project } = focus();
    actor.send({ type: 'OPEN_SESSION', id: 's-1' });

    expect(project()).toEqual({
      name: 'opening',
      request: 1,
      from: { kind: 'none' },
      to: { kind: 'existing', id: 's-1' },
    });
  });

  it('opens a new draft', () => {
    const { actor, project } = focus('s-1');
    actor.send({ type: 'OPEN_NEW' });

    expect(project()).toEqual({
      name: 'opening',
      request: 1,
      from: { kind: 'session', id: 's-1' },
      to: { kind: 'new' },
    });
  });

  it('reports the session an open landed on', () => {
    const { actor, project } = focus();
    actor.send({ type: 'OPEN_SESSION', id: 's-1' });
    actor.send({ type: 'OPEN_SUCCEEDED', request: 1, sessionID: 's-1' });

    expect(project()).toEqual({ name: 'active', sessionID: 's-1' });
  });

  it('reports the failure with where it came from and where it was going', () => {
    const { actor, project } = focus('s-1');
    actor.send({ type: 'OPEN_SESSION', id: 's-2' });
    actor.send({ type: 'OPEN_FAILED', request: 1, error: 'no such session' });

    expect(project()).toEqual({
      name: 'failed',
      request: 1,
      from: { kind: 'session', id: 's-1' },
      to: { kind: 'existing', id: 's-2' },
      error: 'no such session',
    });
  });

  it('falls back to no-session for a state name it does not know', () => {
    const snapshot = {
      value: 'reopening',
      context: {
        request: 4,
        from: { kind: 'none' as const },
        to: null,
        sessionID: 's-1',
        error: '',
      },
    };
    expect(projectFocus(snapshot)).toEqual({ name: 'no-session' });
  });

  it('draws an opening with no target as a new draft', () => {
    const snapshot = {
      value: 'opening',
      context: {
        request: 2,
        from: { kind: 'none' as const },
        to: null,
        sessionID: '',
        error: '',
      },
    };
    expect(projectFocus(snapshot)).toEqual({
      name: 'opening',
      request: 2,
      from: { kind: 'none' },
      to: { kind: 'new' },
    });
  });
});
