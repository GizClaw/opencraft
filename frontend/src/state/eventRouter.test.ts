import { describe, expect, it, vi } from 'vitest';
import type { UIEvent } from '../lib/types';
import { routeBackendEvent, type EventDataSink } from './eventRouter';
import { StateRoot } from './root';

function fakeSink() {
  const calls: string[] = [];
  const sink: EventDataSink = {
    writeConversationData: () => calls.push('conversation-data'),
    writeSubagentStream: () => calls.push('subagent-stream'),
    writeGlobalData: () => calls.push('global-data'),
    refreshSessionList: vi.fn(),
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

  it('keeps delegated subagent streams on the data path', () => {
    const root = new StateRoot();
    const { sink, calls } = fakeSink();
    routeBackendEvent(
      {
        type: 'stream',
        data: { run_id: 'r-sub' },
      },
      { root, data: sink },
    );

    expect(calls).toEqual(['subagent-stream']);
    expect(root.registry.size()).toBe(0);
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
});
