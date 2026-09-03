import { describe, expect, it } from 'vitest';
import { createActor } from 'xstate';
import { sessionFocusMachine } from './sessionFocus';

const start = () => {
  const actor = createActor(sessionFocusMachine, {
    input: { sessionID: '' },
  });
  actor.start();
  return actor;
};

describe('sessionFocus machine', () => {
  it('restores an existing session into active', () => {
    const actor = start();
    actor.send({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    expect(actor.getSnapshot().value).toBe('active');
    expect(actor.getSnapshot().context.sessionID).toBe('s-1');
  });

  it('opens a session and ignores stale results', () => {
    const actor = start();
    actor.send({ type: 'OPEN_SESSION', id: 's-old' });
    actor.send({ type: 'OPEN_SESSION', id: 's-new' });

    actor.send({
      type: 'OPEN_SUCCEEDED',
      request: 1,
      sessionID: 's-old',
    });
    expect(actor.getSnapshot().value).toBe('opening');
    expect(actor.getSnapshot().context.to).toEqual({
      kind: 'existing',
      id: 's-new',
    });

    actor.send({
      type: 'OPEN_SUCCEEDED',
      request: 2,
      sessionID: 's-new',
    });
    expect(actor.getSnapshot().value).toBe('active');
    expect(actor.getSnapshot().context.sessionID).toBe('s-new');
  });

  it('clicking the active session stays active without a new request', () => {
    const actor = start();
    actor.send({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    actor.send({ type: 'OPEN_SESSION', id: 's-1' });
    expect(actor.getSnapshot().value).toBe('active');
    expect(actor.getSnapshot().context.request).toBe(0);
  });

  it('returns to the previous session after a failed switch', () => {
    const actor = start();
    actor.send({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    actor.send({ type: 'OPEN_SESSION', id: 's-2' });
    actor.send({ type: 'OPEN_FAILED', request: 1, error: 'boom' });
    expect(actor.getSnapshot().value).toBe('failed');

    actor.send({ type: 'BACK' });
    expect(actor.getSnapshot().value).toBe('active');
    expect(actor.getSnapshot().context.sessionID).toBe('s-1');
  });

  it('backs to no-session when there was no previous session', () => {
    const actor = start();
    actor.send({ type: 'OPEN_NEW' });
    actor.send({ type: 'OPEN_FAILED', request: 1, error: 'boom' });
    actor.send({ type: 'BACK' });
    expect(actor.getSnapshot().value).toBe('no-session');
  });

  it('workspace reset clears focus state', () => {
    const actor = start();
    actor.send({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    actor.send({ type: 'OPEN_SESSION', id: 's-2' });
    actor.send({ type: 'WORKSPACE_RESET' });
    expect(actor.getSnapshot().value).toBe('no-session');
    expect(actor.getSnapshot().context.request).toBe(0);
  });
});
