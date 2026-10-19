// state/react.ts: the React face of the state root. Two things are worth
// pinning here - what a panel reads (the projection plus the running
// set), and what makes it re-render. The signature helpers exist so a
// stream delta in one conversation, or a run bound to another, does not
// wake every panel that reads this one.

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { stateRoot } from './app';
import {
  conversationWorkspace,
  runningConversationIDs,
  useConversationState,
  useFocusState,
  useRunningConversations,
} from './react';

function open(conversationID: string, workspace?: string) {
  return stateRoot.registry.ensure(conversationID, {
    workspaceGeneration: stateRoot.generation(),
    workspace,
  });
}

function start(conversationID: string, runID: string) {
  const actor = open(conversationID);
  actor?.send({ type: 'SUBMIT', kind: 'send' });
  actor?.send({ type: 'RUN_STARTED', runID });
  return actor;
}

afterEach(() => {
  stateRoot.resetWorkspace();
});

describe('runningConversationIDs', () => {
  it('lists the conversations with a turn in flight, in registry order', () => {
    start('s-1', 'r-1');
    open('s-2');
    open('s-3')?.send({ type: 'SUBMIT', kind: 'send' });

    expect(runningConversationIDs()).toEqual(['s-1', 's-3']);
  });

  it('drops a conversation the moment its turn ends', () => {
    const actor = start('s-1', 'r-1');
    expect(runningConversationIDs()).toEqual(['s-1']);

    actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });

    expect(runningConversationIDs()).toEqual([]);
  });

  it('is the same answer the hook gives', () => {
    start('s-1', 'r-1');
    open('s-2');
    const { result } = renderHook(() => useRunningConversations());

    expect(result.current.map((r) => r.conversationID)).toEqual(['s-1']);
  });
});

describe('conversationWorkspace', () => {
  it('reads the workspace the conversation was opened for', () => {
    open('s-1', '/tmp/a');

    expect(conversationWorkspace('s-1')).toBe('/tmp/a');
    expect(conversationWorkspace('never-opened')).toBeUndefined();
  });
});

describe('useConversationState', () => {
  it('projects one conversation, and nothing for an id it does not hold', () => {
    open('s-1');
    const { result } = renderHook(() => ({
      known: useConversationState('s-1'),
      unknown: useConversationState('s-9'),
    }));

    expect(result.current.known).toMatchObject({
      lifecycle: { name: 'alive' },
      turn: { name: 'idle' },
    });
    expect(result.current.unknown).toBeUndefined();
  });

  it('re-renders on a turn change and stays put on an unrelated bump', () => {
    start('s-1', 'r-1');
    const other = start('s-2', 'r-2');
    let renders = 0;
    const { result } = renderHook(() => {
      renders += 1;
      return useConversationState('s-1');
    });
    expect(renders).toBe(1);

    // Both of these bump the root version without moving anything this
    // panel shows: a run bound to another conversation, and a stage
    // change in that conversation's live turn (the stream path, dozens
    // of deltas a second).
    act(() => {
      stateRoot.registry.bindRun('r-9', 's-2');
      other?.send({ type: 'STREAM', runID: 'r-2', stage: 'thinking' });
    });
    expect(renders).toBe(1);

    act(() => {
      stateRoot.registry.get('s-1')?.send({
        type: 'STREAM',
        runID: 'r-1',
        stage: 'thinking',
      });
    });
    expect(renders).toBe(2);
    expect(result.current?.turn).toEqual({
      name: 'running',
      runID: 'r-1',
      stage: 'thinking',
    });
  });

  it('a panel that reads focus follows it into the focused conversation', () => {
    open('s-1');
    const { result } = renderHook(() => ({
      focus: useFocusState(),
      conversation: useConversationState(),
    }));
    expect(result.current.focus).toEqual({ name: 'no-session' });
    expect(result.current.conversation).toBeUndefined();

    act(() => {
      stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    });

    expect(result.current.focus).toEqual({ name: 'active', sessionID: 's-1' });
    expect(result.current.conversation?.turn).toEqual({ name: 'idle' });
  });
});

describe('useFocusState', () => {
  it('projects the open request, then the session it landed on', () => {
    const { result } = renderHook(() => useFocusState());
    expect(result.current).toEqual({ name: 'no-session' });

    act(() => {
      stateRoot.sendFocus({ type: 'OPEN_SESSION', id: 's-1' });
    });
    expect(result.current).toEqual({
      name: 'opening',
      request: 1,
      from: { kind: 'none' },
      to: { kind: 'existing', id: 's-1' },
    });

    act(() => {
      stateRoot.sendFocus({
        type: 'OPEN_SUCCEEDED',
        request: 1,
        sessionID: 's-1',
      });
    });
    expect(result.current).toEqual({ name: 'active', sessionID: 's-1' });
  });
});

describe('useRunningConversations', () => {
  it('returns each live turn with its projection', () => {
    start('s-1', 'r-1');
    open('s-2')?.send({ type: 'SUBMIT', kind: 'send' });
    const { result } = renderHook(() => useRunningConversations());

    expect(result.current.map((r) => r.conversationID)).toEqual(['s-1', 's-2']);
    expect(result.current[0].state.turn).toEqual({
      name: 'running',
      runID: 'r-1',
      stage: '',
    });
    expect(result.current[1].state.turn).toEqual({ name: 'starting' });
  });

  it('filters by the workspace the actor was opened for', () => {
    start('s-1', 'r-1');
    // Registry actors outlive workspace switches, so a turn keeps running
    // in the workspace that owns it while another one is on screen.
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/b',
    });
    stateRoot.registry.get('s-2')?.send({ type: 'SUBMIT', kind: 'send' });

    const { result, rerender } = renderHook(
      ({ workspace }) => useRunningConversations(workspace),
      { initialProps: { workspace: '/tmp/a' as string | undefined } },
    );
    // s-1 was opened without a workspace, so it is not in /tmp/a either.
    expect(result.current).toEqual([]);

    rerender({ workspace: undefined });
    expect(result.current.map((r) => r.conversationID)).toEqual(['s-1', 's-2']);

    rerender({ workspace: '/tmp/b' });
    expect(result.current.map((r) => r.conversationID)).toEqual(['s-2']);
  });
});
