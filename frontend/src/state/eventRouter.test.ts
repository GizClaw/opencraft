import { describe, expect, it, vi } from 'vitest';
import {
  canApplyEvent,
  isDelegatedStream,
  routeBackendEvent,
  toConversationEvent,
  type EventDataSink,
} from './eventRouter';
import { StateRoot } from './root';

function fakeSink() {
  const calls: string[] = [];
  const sink: EventDataSink = {
    writeConversationData: () => calls.push('conversation-data'),
    writeGlobalData: () => calls.push('global-data'),
    refreshSessionList: vi.fn(),
    sessionUpdated: vi.fn(),
    openConversation: vi.fn(),
    refreshAutomations: vi.fn(),
    refreshAutomationRuns: vi.fn(),
    conversationForRunID: () => undefined,
    pendingInteractConversation: () => undefined,
  };
  return { sink, calls };
}

describe('event router', () => {
  it('routes automation_run_started to a conversation actor', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-1', conversation_id: 's-1' },
      },
      { root, data: sink },
    );

    const actor = root.registry.get('s-1');
    expect(actor).toBeDefined();
    expect(actor?.getSnapshot().value).toMatchObject({
      lifecycle: 'alive',
      turn: 'running',
    });
  });

  it('routes turn_end into the actor and writes data first', () => {
    const root = new StateRoot();
    root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-1', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          status: 'completed',
        },
      },
      { root, data: sink },
    );

    const actor = root.registry.get('s-1');
    expect(actor?.getSnapshot().value).toMatchObject({
      turn: 'succeeded',
    });
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(2);
  });

  // "unknown" is the engine status of a run whose wait produced no result
  // — the host records that run as failed (Go's ClassifySettledRun). A
  // router that read anything-but-failed as success would draw a turn
  // that never finished as an answer, and drop the error_kind the
  // failure text is chosen from.
  it('reads a status outside the terminal set as a failure', () => {
    const root = new StateRoot();
    root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    const { sink } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-1', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          status: 'unknown',
          error: 'the run produced no result',
          error_kind: 'timeout',
        },
      },
      { root, data: sink },
    );

    const snapshot = root.registry.get('s-1')?.getSnapshot();
    expect(snapshot?.value).toMatchObject({ turn: 'failed' });
    expect(snapshot?.context).toMatchObject({
      turnError: 'the run produced no result',
      failureErrorKind: 'timeout',
    });
  });

  it('uses conversation_id from resolved before the pending index', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'interact',
        data: {
          id: 'p-1',
          run_id: 'r-1',
          conversation_id: 's-1',
        },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'resolved',
        data: { id: 'p-1', conversation_id: 's-1' },
      },
      { root, data: sink },
    );

    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(2);
  });

  // The shell routes a fixed set of events; the rest belong to
  // components that subscribe to the channel themselves. An unlisted one
  // used to disappear without a trace, so a renamed wire event looked
  // like a feature that quietly stopped working.
  it('says so when an event has no route, and not for a component event', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    try {
      routeBackendEvent(
        { type: 'git_changed', data: {} },
        { root, data: sink },
      );
      expect(warn).not.toHaveBeenCalled();

      routeBackendEvent(
        { type: 'no_such_event', data: {} },
        { root, data: sink },
      );
      expect(warn).toHaveBeenCalledTimes(1);
      expect(String(warn.mock.calls[0][0])).toContain('no_such_event');
    } finally {
      warn.mockRestore();
    }
  });

  // The three events that carry a run say which workspace owns it; the
  // window's own workspace is only the fallback. A run started in a
  // background workspace reaches this client while the window is
  // looking elsewhere, and an actor registered under the wrong
  // workspace files its row under the wrong project (which is also
  // where a Mod+N digit then leads).
  it('registers a conversation under the workspace its event names', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          workspace: '/tmp/background',
          delta: { part: { type: 'text', text: 'hi' } },
        },
      },
      { root, data: { ...sink, activeWorkspace: () => '/tmp/on-screen' } },
    );

    expect(root.workspaceOf('s-1')).toBe('/tmp/background');
  });

  it('rebinds an actor registered under another workspace', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    // ensure() is get-or-create: an actor made before the backend named
    // a workspace keeps the options it was created with, so the event
    // that knows better has to move it.
    root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
      workspace: '/tmp/on-screen',
    });
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          status: 'completed',
          workspace: '/tmp/background',
        },
      },
      { root, data: sink },
    );

    expect(root.workspaceOf('s-1')).toBe('/tmp/background');
  });

  it('falls back to the active workspace for an event that names none', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      {
        type: 'turn_end',
        data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
      },
      { root, data: { ...sink, activeWorkspace: () => '/tmp/on-screen' } },
    );

    expect(root.workspaceOf('s-1')).toBe('/tmp/on-screen');
  });

  it('rebinds once per change, not once per delta', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
      workspace: '/tmp/on-screen',
    });
    const actor = root.registry.get('s-1');
    if (!actor) throw new Error('actor was not created');
    const binds: string[] = [];
    const send = actor.send;
    // xstate actors are plain objects: wrapping send counts the
    // transitions the router drives without changing what they do.
    actor.send = ((event: { type: string; workspace?: string }) => {
      if (event.type === 'BIND_WORKSPACE') binds.push(event.workspace ?? '');
      return send.call(actor, event as never);
    }) as typeof actor.send;

    for (let i = 0; i < 3; i += 1) {
      routeBackendEvent(
        {
          type: 'stream',
          data: {
            run_id: 'r-1',
            conversation_id: 's-1',
            workspace: '/tmp/background',
            delta: { part: { type: 'text', text: `t${i}` } },
          },
        },
        { root, data: sink },
      );
    }

    // The first delta is news; the two after it name the workspace the
    // actor already carries, and a bind per delta would re-enter the
    // machine on a path that only exists to pin metadata.
    expect(binds).toEqual(['/tmp/background']);
  });

  it('tombstoned conversations never reach data or actors', () => {
    const root = new StateRoot();
    root.registry.markDeleted('s-1');
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'stream',
        data: { run_id: 'r-late', conversation_id: 's-1' },
      },
      { root, data: sink },
    );

    expect(calls).toEqual([]);
    expect(root.registry.get('s-1')).toBeUndefined();
  });

  it('routes steer_pending to the data layer without touching the turn', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-1', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'steer_pending',
        data: { run_id: 'r-1', conversation_id: 's-1', steer_pending: 0 },
      },
      { root, data: sink },
    );

    // The count is the data layer's: it settles interjection rows that are
    // still waiting, and the turn's own state does not move for it.
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(2);
    expect(root.registry.get('s-1')?.getSnapshot().value).toMatchObject({
      turn: 'running',
    });
  });

  it('hands session_updated to the data layer with its conversation id', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      { type: 'session_updated', data: { id: 's-9' } },
      { root, data: sink },
    );

    // The list refresh and the transcript tail check are two different
    // repairs: the sidebar needs the new order, an open conversation
    // needs the turns the app appended on its own.
    expect(sink.sessionUpdated).toHaveBeenCalledWith('s-9');
    expect(sink.refreshSessionList).toHaveBeenCalled();
  });

  it('ignores a session_updated without a conversation id', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      { type: 'session_updated', data: {} },
      { root, data: sink },
    );

    expect(sink.sessionUpdated).not.toHaveBeenCalled();
  });

  // The banner click is an intent, not a turn: it names a conversation
  // and the workspace that owns it, and the data layer does the
  // switching. No actor is created here — the conversation may not even
  // be loaded in this window.
  it('hands a banner click to the data layer with its workspace', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      {
        type: 'open_conversation',
        data: { conversation_id: 's-9', workspace: '/tmp/background' },
      },
      { root, data: sink },
    );

    expect(sink.openConversation).toHaveBeenCalledWith(
      's-9',
      '/tmp/background',
    );
    expect(root.registry.get('s-9')).toBeUndefined();
  });

  it('ignores a banner click without a conversation id', () => {
    const root = new StateRoot();
    const { sink } = fakeSink();
    routeBackendEvent(
      { type: 'open_conversation', data: { workspace: '/tmp/background' } },
      { root, data: sink },
    );

    expect(sink.openConversation).not.toHaveBeenCalled();
  });

  // The gate between the backend and both state and data is "does this
  // event still belong to the conversation", and the answer cannot be
  // read off the turn phase: a phase only ever describes the runs this
  // client knows about. A scheduled automation pinned to the
  // conversation starts its own run whenever its schedule fires, so the
  // first thing the UI hears about it can arrive while the last turn
  // rests on `succeeded` — and gating on the phase dropped the run's
  // whole live turn, leaving the transcript to a later session_updated.
  it('lets a new run through a conversation whose last turn succeeded', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-1', conversation_id: 's-1', message: 'go' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
      },
      { root, data: sink },
    );
    expect(root.registry.get('s-1')?.getSnapshot().value).toMatchObject({
      turn: 'succeeded',
    });

    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-2', conversation_id: 's-1', message: 'again' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'r-2',
          conversation_id: 's-1',
          delta: { part: { type: 'text' } },
        },
      },
      { root, data: sink },
    );

    expect(root.registry.get('s-1')?.getSnapshot().value).toMatchObject({
      turn: 'running',
    });
    // Every event reached the data layer as well: the live transcript of
    // the automation's turn is the part that used to go missing.
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(4);
  });

  it('drops events whose run has already ended', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    for (const runID of ['r-1', 'r-1']) {
      routeBackendEvent(
        {
          type: 'turn_end',
          data: { run_id: runID, conversation_id: 's-1', status: 'completed' },
        },
        { root, data: sink },
      );
    }

    // The first one is the turn's own ending; the second is a replay of a
    // run the actor has finished with, and the data layer's reconciliation
    // must not run for it twice. A late delta of the same run is the other
    // stale shape, and it is dropped the same way.
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(1);

    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { part: { type: 'text', text: 'late' } },
        },
      },
      { root, data: sink },
    );
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(1);
  });

  // A delegated subagent streams into the conversation that spawned it,
  // but it is not a turn of it: this window only ever gets a turn_end
  // for the run it started, and an async subagent outlives that run by
  // minutes. Folding its deltas in reopened the settled turn as
  // `running` under a run nothing would ever settle — the "Working…"
  // that never stopped — and wrote the subagent's words into the
  // caller's answer.
  it('never lets a delegated run reopen a settled turn', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-parent', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-parent',
          conversation_id: 's-1',
          status: 'completed',
        },
      },
      { root, data: sink },
    );
    const settled = calls.filter((c) => c === 'conversation-data').length;

    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'subagent:card-1',
          parent_run_id: 'r-parent',
          conversation_id: 's-1',
          delta: { part: { type: 'text', text: 'subagent work' } },
        },
      },
      { root, data: sink },
    );

    const snapshot = root.registry.get('s-1')?.getSnapshot();
    expect(snapshot?.value).toMatchObject({ turn: 'succeeded' });
    expect(snapshot?.context).toMatchObject({ currentRunID: undefined });
    // Nothing was written either: the data layer opens the phantom strip
    // this test exists to prevent.
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(
      settled,
    );
  });

  // The dock and the pet subscribe to the channel themselves, so a
  // delegated delta still has to be answerable — it just must not create
  // window state on its way past: an actor registered for a conversation
  // the window never opened would file that conversation under whatever
  // workspace happened to be active.
  it('does not register an actor for a delegated stream', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'subagent:card-1',
          parent_run_id: 'r-parent',
          conversation_id: 's-ghost',
          delta: { part: { type: 'text', text: 'hi' } },
        },
      },
      { root, data: sink },
    );

    expect(root.registry.get('s-ghost')).toBeUndefined();
    expect(calls).toEqual([]);
  });

  // A barge-in replaces a run without that run ever becoming the last
  // one to end - the replacement's own ending is. Judging staleness by
  // one slot let the replaced run's late delta back in, where it opened
  // a live strip for a run whose terminal event had already been
  // consumed and left the turn running forever.
  it('drops a delta from a run a barge-in replaced', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-old', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'automation_run_started',
        data: { run_id: 'r-new', conversation_id: 's-1' },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-old',
          conversation_id: 's-1',
          status: 'interrupted',
        },
      },
      { root, data: sink },
    );
    routeBackendEvent(
      {
        type: 'turn_end',
        data: {
          run_id: 'r-new',
          conversation_id: 's-1',
          status: 'completed',
        },
      },
      { root, data: sink },
    );
    expect(root.registry.get('s-1')?.getSnapshot().value).toMatchObject({
      turn: 'succeeded',
    });

    const before = calls.filter((c) => c === 'conversation-data').length;
    routeBackendEvent(
      {
        type: 'stream',
        data: {
          run_id: 'r-old',
          conversation_id: 's-1',
          delta: { part: { type: 'text', text: 'late' } },
        },
      },
      { root, data: sink },
    );

    // Neither the data layer nor the actor sees it.
    expect(calls.filter((c) => c === 'conversation-data')).toHaveLength(before);
    expect(root.registry.get('s-1')?.getSnapshot().value).toMatchObject({
      turn: 'succeeded',
    });
  });
});

describe('toConversationEvent', () => {
  const stageOf = (part: Record<string, unknown>) =>
    (
      toConversationEvent({
        type: 'stream',
        data: { run_id: 'r-1', delta: { part } },
      }) as { stage?: string }
    ).stage;

  it('names the stage a stream delta is in', () => {
    expect(stageOf({ type: 'reasoning' })).toBe('reasoning');
    expect(stageOf({ type: 'text' })).toBe('text');
    expect(stageOf({ type: 'tool_call', call: { name: 'read_file' } })).toBe(
      'tool:read_file',
    );
    // A call the delta did not name still says it is a tool call: the
    // stage drives the label, and a missing name must not erase it.
    expect(stageOf({ type: 'tool_call' })).toBe('tool:');
    expect(stageOf({ type: 'artifact' })).toBeUndefined();
    expect(stageOf({})).toBeUndefined();
  });

  it('drops the events a run id is missing from', () => {
    expect(
      toConversationEvent({ type: 'stream', data: { delta: {} } }),
    ).toBeUndefined();
    expect(
      toConversationEvent({ type: 'turn_end', data: { status: 'completed' } }),
    ).toBeUndefined();
    expect(
      toConversationEvent({ type: 'automation_run_started', data: {} }),
    ).toBeUndefined();
  });

  // The producer already says which run delegated this one; the router
  // is where that turns into "this is not a turn of that conversation"
  // (see isDelegatedStream). Only a stream can be delegated: the
  // delegated run's own ending never reaches this conversation.
  it('never maps a delegated stream to a turn event', () => {
    expect(
      toConversationEvent({
        type: 'stream',
        data: {
          run_id: 'subagent:card-1',
          parent_run_id: 'r-parent',
          delta: { part: { type: 'text' } },
        },
      }),
    ).toBeUndefined();
    // A run id equal to its own parent is a malformed edge, not a
    // delegation: the field says the run has a caller, so it is dropped
    // all the same.
    expect(
      isDelegatedStream({
        type: 'stream',
        data: { run_id: 'r-1', parent_run_id: 'r-1' },
      }),
    ).toBe(true);
    expect(
      isDelegatedStream({
        type: 'stream',
        data: { run_id: 'r-1', parent_run_id: '' },
      }),
    ).toBe(false);
    expect(
      isDelegatedStream({ type: 'turn_end', data: { run_id: 'r-1' } }),
    ).toBe(false);
  });

  it('maps the wire status and carries the error and its kind', () => {
    expect(
      toConversationEvent({
        type: 'turn_end',
        data: { run_id: 'r-1', status: 'completed' },
      }),
    ).toEqual({
      type: 'TURN_ENDED',
      runID: 'r-1',
      status: 'completed',
      error: undefined,
      errorKind: undefined,
    });

    // "unknown" is the engine status of a wait that produced no result.
    // It is a failure, and it keeps the error_kind the failure text is
    // chosen from - a deadline and a user stop share the status.
    expect(
      toConversationEvent({
        type: 'turn_end',
        data: {
          run_id: 'r-1',
          status: 'unknown',
          error: 'no answer',
          error_kind: 'timeout',
        },
      }),
    ).toEqual({
      type: 'TURN_ENDED',
      runID: 'r-1',
      status: 'failed',
      error: 'no answer',
      errorKind: 'timeout',
    });
  });

  it('leaves the data-only events to the data layer', () => {
    expect(
      toConversationEvent({ type: 'artifact', data: { run_id: 'r-1' } }),
    ).toBeUndefined();
    expect(
      toConversationEvent({ type: 'steer_pending', data: { run_id: 'r-1' } }),
    ).toBeUndefined();
  });
});

describe('canApplyEvent', () => {
  const alive = { lifecycle: { name: 'alive' as const } };
  const endedTurn = {
    type: 'turn_end',
    data: { run_id: 'r-1', status: 'completed' },
  };

  it("lets a run's own ending through and drops the replay", () => {
    expect(canApplyEvent(alive, endedTurn)).toBe(true);
    expect(canApplyEvent({ ...alive, endedRunIDs: ['r-1'] }, endedTurn)).toBe(
      false,
    );
  });

  it('asks the ended-run set, not a single slot', () => {
    // A barge-in leaves an older ending behind without it ever becoming
    // the last one; both runs stay remembered, so neither can be
    // resurrected by a late delta.
    const ended = { ...alive, endedRunIDs: ['r-old', 'r-new'] };
    expect(
      canApplyEvent(ended, { type: 'stream', data: { run_id: 'r-old' } }),
    ).toBe(false);
    expect(
      canApplyEvent(ended, { type: 'stream', data: { run_id: 'r-live' } }),
    ).toBe(true);
  });

  it('lets an event with no run id through', () => {
    expect(canApplyEvent(alive, { type: 'stream', data: {} })).toBe(true);
  });

  it('drops everything once the conversation is deleted', () => {
    const deleted = { lifecycle: { name: 'deleted' as const } };

    expect(
      canApplyEvent(deleted, { type: 'stream', data: { run_id: 'r-1' } }),
    ).toBe(false);
    expect(canApplyEvent(deleted, endedTurn)).toBe(false);
  });

  it('has no say over the data-only events', () => {
    // artifact/interact/resolved are reconciled by the data layer; the
    // actor never sees them, so the gate must not filter them.
    expect(
      canApplyEvent(alive, { type: 'artifact', data: { run_id: 'r-1' } }),
    ).toBe(true);
  });
});
