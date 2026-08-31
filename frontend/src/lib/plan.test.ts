import { describe, expect, it } from 'vitest';
import { latestPlan } from './plan';
import type { AssistantItem, MessageView } from './store';

function planCall(
  id: string,
  args: string,
  status: 'running' | 'done' = 'done',
): AssistantItem {
  return {
    kind: 'tool_call',
    id,
    tool: { id, name: 'update_plan', args, status },
  };
}

function msg(id: string, items: AssistantItem[]): MessageView {
  return { id, role: 'assistant', text: '', items, attachments: [] };
}

const validArgs = JSON.stringify({
  explanation: 'fix the bug',
  plan: [{ step: 'repro' }, { step: 'patch' }],
});

describe('latestPlan', () => {
  it('returns null when there is no update_plan call', () => {
    const m = msg('m', [
      { kind: 'text', id: 't', text: 'hi' },
      {
        kind: 'tool_call',
        id: 'x',
        tool: { id: 'x', name: 'exec_command', args: '{}', status: 'done' },
      },
    ]);
    expect(latestPlan([m])).toBeNull();
  });

  it('returns the snapshot of a completed plan call', () => {
    const m = msg('m', [planCall('p', validArgs)]);
    expect(latestPlan([m])).toEqual({
      plan: {
        explanation: 'fix the bug',
        items: [{ step: 'repro' }, { step: 'patch' }],
      },
      live: false,
    });
  });

  it('marks the plan as live while the call is running', () => {
    const m = msg('m', [planCall('p', validArgs, 'running')]);
    expect(latestPlan([m])?.live).toBe(true);
  });

  it('returns the newest valid snapshot when multiple calls exist', () => {
    const older = msg('m1', [planCall('p1', validArgs)]);
    const newerArgs = JSON.stringify({
      plan: [{ step: 'v2' }],
    });
    const newer = msg('m2', [planCall('p2', newerArgs)]);
    expect(latestPlan([older, newer])?.plan).toEqual({
      items: [{ step: 'v2' }],
    });
  });

  it('falls back to the previous valid snapshot when the newest call has empty args', () => {
    const older = msg('m1', [planCall('p1', validArgs)]);
    const streaming = msg('m2', [planCall('p2', '', 'running')]);
    const state = latestPlan([older, streaming]);
    expect(state?.plan).toEqual({
      explanation: 'fix the bug',
      items: [{ step: 'repro' }, { step: 'patch' }],
    });
    expect(state?.live).toBe(true);
  });

  it('falls back when the newest args are malformed JSON', () => {
    const older = msg('m1', [planCall('p1', validArgs)]);
    const bad = msg('m2', [planCall('p2', '{not json', 'running')]);
    expect(latestPlan([older, bad])?.plan.items).toEqual([
      { step: 'repro' },
      { step: 'patch' },
    ]);
  });

  it('treats an empty plan array as invalid and falls back', () => {
    const older = msg('m1', [planCall('p1', validArgs)]);
    const empty = msg('m2', [planCall('p2', '{"plan":[]}', 'running')]);
    expect(latestPlan([older, empty])?.plan).toEqual({
      explanation: 'fix the bug',
      items: [{ step: 'repro' }, { step: 'patch' }],
    });
  });

  it('returns an empty placeholder plan when only the live call exists', () => {
    const m = msg('m', [planCall('p', '', 'running')]);
    expect(latestPlan([m])).toEqual({
      plan: { items: [] },
      live: true,
    });
  });

  it('omits explanation when the snapshot has none', () => {
    const m = msg('m', [
      planCall('p', JSON.stringify({ plan: [{ step: 'a' }] })),
    ]);
    expect(latestPlan([m])?.plan).toEqual({ items: [{ step: 'a' }] });
  });
});
