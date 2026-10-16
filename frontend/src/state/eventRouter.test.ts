import { describe, expect, it, vi } from 'vitest';
import { routeBackendEvent, type EventDataSink } from './eventRouter';
import { StateRoot } from './root';

function fakeSink() {
  const calls: string[] = [];
  const sink: EventDataSink = {
    writeConversationData: () => calls.push('conversation-data'),
    writeGlobalData: () => calls.push('global-data'),
    refreshSessionList: vi.fn(),
    sessionUpdated: vi.fn(),
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
