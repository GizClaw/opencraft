// What the dispatcher does with a command once a key has picked one. The
// session slots get their own file: their targeting rule lives in
// lib/sessionSlots.ts (numbered there, dispatched here through the same
// call), and the two reads have to agree about *live* state — a turn that
// started since the last render leads the list already, or the digit would
// land on the row below the number the user read off the screen.
import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useShellCommands } from './shellCommands';
import { useStore } from './store';
import type { SessionMeta } from './types';
import { stateRoot } from '../state/app';

const workspace = '/tmp/slots';

function meta(id: string): SessionMeta {
  return {
    id,
    title: id,
    created_at: '2026-09-06T00:00:00Z',
    updated_at: '2026-09-06T00:00:00Z',
    total_tokens: 0,
    turns: 1,
    messages: 1,
  };
}

describe('session slots', () => {
  const resume = vi.fn<(id: string) => Promise<void>>();

  beforeEach(() => {
    stateRoot.resetWorkspace();
    resume.mockReset();
    resume.mockImplementation(async () => {});
    useStore.setState({
      workspace,
      sessions: [meta('s-1'), meta('s-2'), meta('s-3')],
      pendingPromptConvs: {},
      resume,
    });
  });

  it('resumes the row the sidebar numbers with that digit', () => {
    const run = renderHook(() => useShellCommands()).result.current;

    run('session.slot2');

    expect(resume).toHaveBeenCalledExactlyOnceWith('s-2');
  });

  it('follows a running row onto the first slot and pushes the rest down', () => {
    // A conversation that is running leads the sidebar's list, so the
    // digits follow it: slot 1 is the running row, not the newest stored
    // one (lib/sessionSlots.ts).
    const actor = stateRoot.registry.ensure('s-run', {
      workspaceGeneration: stateRoot.generation(),
      workspace,
    });
    actor?.send({ type: 'NEW_CHAT_READY' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-run' });

    const run = renderHook(() => useShellCommands()).result.current;
    run('session.slot1');
    expect(resume).toHaveBeenCalledExactlyOnceWith('s-run');

    resume.mockClear();
    run('session.slot2');
    expect(resume).toHaveBeenCalledExactlyOnceWith('s-1');
  });

  it('leaves an empty slot and the open session alone', () => {
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    useStore.setState({ sessions: [meta('s-1')] });

    const run = renderHook(() => useShellCommands()).result.current;

    // Nothing is numbered 2 anymore, and a digit that names what is
    // already open must not re-open it: resume() on the current session
    // closes the surfaces the user was looking at.
    run('session.slot2');
    run('session.slot1');
    expect(resume).not.toHaveBeenCalled();
  });
});
