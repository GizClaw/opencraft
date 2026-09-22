import { describe, expect, it } from 'vitest';
import { latestPlan, planNeedsRefresh } from './plan';
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
      id: 'p',
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
    const state = latestPlan([older, newer]);
    expect(state?.plan).toEqual({ items: [{ step: 'v2' }] });
    // The id is the newest call's: a revision of that plan is content
    // arriving in the checklist the reader already has a fold for, while
    // another call is a new one (see PlanSection).
    expect(state?.id).toBe('p2');
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
    // The fallback is the previous snapshot's content, but the plan is
    // the streaming call: its id opens the section for the checklist
    // about to arrive.
    expect(state?.id).toBe('p2');
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
      id: 'p',
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

describe('planNeedsRefresh', () => {
  it('skips plain text deltas that do not touch a plan call', () => {
    const plan = planCall('p', validArgs, 'running');
    const before = msg('m1', [plan]);
    const after: MessageView = {
      ...before,
      items: [...before.items, { kind: 'text', id: 't1', text: 'streaming' }],
    };
    expect(planNeedsRefresh([before], [after])).toBe(false);
  });

  it('refreshes when a plan call transitions from running to done', () => {
    const before = msg('m1', [planCall('p', validArgs, 'running')]);
    const after = msg('m1', [planCall('p', validArgs)]);
    expect(planNeedsRefresh([before], [after])).toBe(true);
  });

  it('refreshes when a new plan call appears', () => {
    const before = msg('m1', []);
    const after = msg('m1', [planCall('p', validArgs)]);
    expect(planNeedsRefresh([before], [after])).toBe(true);
  });

  it('refreshes when a plan-bearing message is truncated away', () => {
    const before = msg('m1', [planCall('p', validArgs)]);
    expect(planNeedsRefresh([before], [])).toBe(true);
  });
});
