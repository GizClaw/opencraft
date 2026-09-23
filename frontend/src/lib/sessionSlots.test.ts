// The session slots' one definition (see the comment at the top of
// sessionSlots.ts): the order the sidebar draws sits here, because the
// sidebar numbers its rows with it and the dispatcher jumps by it.
import { beforeEach, describe, expect, it } from 'vitest';
import { stateRoot } from '../state/app';
import { runningIDsByWorkspace, sessionSlotIDs } from './sessionSlots';
import type { SessionMeta } from './types';

function meta(id: string): SessionMeta {
  return {
    id,
    title: id,
    created_at: '2026-09-06T00:00:00Z',
    updated_at: '2026-09-06T00:00:00Z',
    turns: 1,
    messages: 1,
    total_tokens: 0,
  };
}

/** running opens an actor for `id` in `workspace` and starts its turn, the
 * way the sidebar tests do. */
function running(id: string, workspace: string): void {
  const actor = stateRoot.registry.ensure(id, {
    workspaceGeneration: stateRoot.generation(),
    workspace,
  });
  actor?.send({ type: 'NEW_CHAT_READY' });
  actor?.send({ type: 'RUN_STARTED', runID: `r-${id}` });
}

/** opened opens an actor without starting a turn (a conversation that is
 * only waiting on the user). */
function opened(id: string, workspace: string): void {
  stateRoot.registry.ensure(id, {
    workspaceGeneration: stateRoot.generation(),
    workspace,
  });
}

beforeEach(() => {
  stateRoot.resetWorkspace();
});

describe('sessionSlotIDs', () => {
  it('numbers the stored rows in the order they arrive', () => {
    expect(sessionSlotIDs([meta('a'), meta('b'), meta('c')], [])).toEqual([
      'a',
      'b',
      'c',
      undefined,
    ]);
  });

  it('leaves a slot past the end of the list empty', () => {
    expect(sessionSlotIDs([meta('a'), meta('b')], [])).toEqual([
      'a',
      'b',
      undefined,
      undefined,
    ]);
  });

  it('leads with the running conversations, then the stored rows', () => {
    // The running row is not in the stored list: it may not have an
    // archive row yet, and it is drawn above them either way.
    expect(sessionSlotIDs([meta('b'), meta('c')], ['a'])).toEqual([
      'a',
      'b',
      'c',
      undefined,
    ]);
  });

  it('counts a running row once when it is also stored', () => {
    expect(sessionSlotIDs([meta('a'), meta('b')], ['a'])).toEqual([
      'a',
      'b',
      undefined,
      undefined,
    ]);
  });

  it('stops at the number of slots the keyboard binds', () => {
    const slots = sessionSlotIDs(['a', 'b', 'c', 'd', 'e'].map(meta), []);
    expect(slots).toEqual(['a', 'b', 'c', 'd']);
  });
});

describe('runningIDsByWorkspace', () => {
  it('groups the live conversations by the workspace that owns them', () => {
    running('a1', '/tmp/a');
    running('b1', '/tmp/b');
    expect(runningIDsByWorkspace(['a1', 'b1'], {})).toEqual({
      '/tmp/a': ['a1'],
      '/tmp/b': ['b1'],
    });
  });

  it('lists a conversation waiting on a prompt after the running ones', () => {
    running('a1', '/tmp/a');
    opened('a2', '/tmp/a');
    expect(runningIDsByWorkspace(['a1'], { 'i-1': 'a2' })).toEqual({
      '/tmp/a': ['a1', 'a2'],
    });
  });

  it('does not list a waiting conversation twice', () => {
    running('a1', '/tmp/a');
    expect(runningIDsByWorkspace(['a1'], { 'i-1': 'a1' })).toEqual({
      '/tmp/a': ['a1'],
    });
  });

  it('drops ids no actor holds', () => {
    // A hostless run (an automation with no window) is in no workspace,
    // exactly as in the sidebar.
    expect(runningIDsByWorkspace(['ghost'], { 'i-1': 'ghost' })).toEqual({});
  });
});
