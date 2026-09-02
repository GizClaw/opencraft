import { describe, expect, it } from 'vitest';
import {
  activeSessionID,
  displaySessionID,
  navigationReducer,
  type SessionNavigation,
} from './sessionMachine';

describe('session navigation reducer', () => {
  it('switches to ready with the requested session', () => {
    const state = navigationReducer(
      { name: 'idle', epoch: 0 },
      { type: 'ready', sessionID: 's-1', epoch: 1 },
    );
    expect(state).toEqual({ name: 'ready', sessionID: 's-1', epoch: 1 });
    expect(activeSessionID(state)).toBe('s-1');
  });

  it('keeps the previous session visible while switching', () => {
    const ready: SessionNavigation = {
      name: 'ready',
      sessionID: 's-old',
      epoch: 1,
    };
    const switching = navigationReducer(ready, {
      type: 'switch',
      kind: 'new',
      previousSessionID: 's-old',
      epoch: 2,
    });
    expect(activeSessionID(switching)).toBe('');
    expect(displaySessionID(switching)).toBe('s-old');
  });

  it('returns to the previous session after a failed switch', () => {
    const failed = navigationReducer(
      { name: 'ready', sessionID: 's-old', epoch: 1 },
      {
        type: 'fail',
        epoch: 2,
        previousSessionID: 's-old',
        error: 'boom',
      },
    );
    expect(failed).toMatchObject({ name: 'failed', error: 'boom' });
    expect(displaySessionID(failed)).toBe('s-old');
    expect(activeSessionID(failed)).toBe('');
  });

  it('covers the legal transition matrix', () => {
    const idle = { name: 'idle' as const, epoch: 0 };
    const ready: SessionNavigation = {
      name: 'ready',
      sessionID: 's-old',
      epoch: 1,
    };
    const switching = navigationReducer(ready, {
      type: 'switch',
      kind: 'new',
      previousSessionID: 's-old',
      epoch: 2,
    });
    const failed = navigationReducer(switching, {
      type: 'fail',
      epoch: 3,
      previousSessionID: 's-old',
      error: 'boom',
    });

    // idle -> ready (startup restore)
    expect(
      navigationReducer(idle, {
        type: 'ready',
        sessionID: 's-1',
        epoch: 1,
      }),
    ).toMatchObject({ name: 'ready', sessionID: 's-1' });

    // ready -> switching (user opens history / new chat)
    expect(
      navigationReducer(ready, {
        type: 'switch',
        kind: 'resume',
        targetID: 's-2',
        previousSessionID: 's-old',
        epoch: 2,
      }),
    ).toMatchObject({ name: 'switching', targetID: 's-2' });

    // switching -> ready
    expect(
      navigationReducer(switching, {
        type: 'ready',
        sessionID: 's-new',
        epoch: 3,
      }),
    ).toMatchObject({ name: 'ready', sessionID: 's-new' });

    // switching -> failed (rollback visible)
    expect(failed).toMatchObject({ name: 'failed', error: 'boom' });
    expect(displaySessionID(failed)).toBe('s-old');

    // failed -> switching (retry from error)
    expect(
      navigationReducer(failed, {
        type: 'switch',
        kind: 'resume',
        targetID: 's-2',
        previousSessionID: 's-old',
        epoch: 4,
      }),
    ).toMatchObject({ name: 'switching', targetID: 's-2' });

    // any state -> idle (workspace reset)
    for (const state of [idle, ready, switching, failed]) {
      expect(navigationReducer(state, { type: 'idle', epoch: 99 })).toEqual({
        name: 'idle',
        epoch: 99,
      });
    }
  });
});
