// conv/attention.test.ts: the sidebar's memory of a turn that ended on
// the reader.
//
// The map is store state rather than actor state because a row has to be
// right for conversations this window never opened - a turn that ran in
// another workspace, or one that finished before the reader looked.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store';
import { stateRoot } from '../../state/app';
import { bindRun, primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

/** settleTurnEnd hands the event to the store and lets the tail sync the
 * store starts for it fail quietly (the archive mock has no such run). */
async function settleTurnEnd(data: {
  run_id?: string;
  conversation_id?: string;
  status?: string;
  error_kind?: string;
}) {
  useStore.getState().handleEvent({ type: 'turn_end', data });
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('turn attention', () => {
  it('keeps the run of the turn that needs the reader', async () => {
    await settleTurnEnd({
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'failed',
      error_kind: 'provider_failure',
    });

    // The run id is kept next to the conversation so a later surface can
    // say which turn it was, not just that something once went wrong.
    expect(useStore.getState().turnAttention).toEqual({ 's-1': 'r-1' });
  });

  it('clears the mark when the last turn ends another way', async () => {
    await settleTurnEnd({
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'failed',
    });
    await settleTurnEnd({
      run_id: 'r-2',
      conversation_id: 's-1',
      status: 'completed',
    });

    // "The last turn that ended": a retry that worked takes the ❌ away
    // by itself, without the reader having to open the row.
    expect(useStore.getState().turnAttention).toEqual({});
  });

  it('reads a user stop as an ending, not a failure', async () => {
    await settleTurnEnd({
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'failed',
    });
    await settleTurnEnd({
      run_id: 'r-2',
      conversation_id: 's-1',
      status: 'canceled',
    });

    // The reader pressed Stop; the row does not ask them to come back to
    // something they just did.
    expect(useStore.getState().turnAttention).toEqual({});
  });

  it('marks a run a deadline cut off', async () => {
    await settleTurnEnd({
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'canceled',
      error_kind: 'timeout',
    });

    // Same wire status as a stop, different story: the reader did not do
    // this, and the summary they were waiting for does not exist.
    expect(useStore.getState().turnAttention).toEqual({ 's-1': 'r-1' });
  });

  it('marks a conversation this window never opened', async () => {
    // No actor, no transcript entry: the turn ran in another workspace.
    // The id in the event is enough to put the mark on the right row.
    await settleTurnEnd({
      run_id: 'r-9',
      conversation_id: 's-elsewhere',
      status: 'failed',
    });

    expect(useStore.getState().turnAttention).toEqual({
      's-elsewhere': 'r-9',
    });
  });

  it('resolves the conversation from a bound run when the event names none', async () => {
    bindRun('r-7', 's-1');

    await settleTurnEnd({ run_id: 'r-7', status: 'aborted' });

    // Older backends sent only the run; the registry is the same index
    // the router uses to find the conversation for it.
    expect(useStore.getState().turnAttention).toEqual({ 's-1': 'r-7' });
  });

  it('marks nothing for a turn it cannot attribute', async () => {
    await settleTurnEnd({ status: 'failed' });

    // Without a conversation or a run there is no row to mark; inventing
    // one would put the ❌ on whatever conversation happened to be open.
    expect(useStore.getState().turnAttention).toEqual({});
  });

  it('leaves a conversation deleted in this process unmarked', async () => {
    stateRoot.registry.markDeleted('s-1');

    await settleTurnEnd({
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'failed',
    });

    // The row is gone and its id is retired, so nothing would ever draw
    // this mark or clear it: a late ending for a deleted chat is the one
    // write that has no reader.
    expect(useStore.getState().turnAttention).toEqual({});
  });
});
