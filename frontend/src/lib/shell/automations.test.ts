// shell/automations.ts: the two sequenced lists the views poll.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AutomationRun, AutomationTask } from '../types';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import { primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('automation lists', () => {
  const task = (id: string): AutomationTask => ({
    id,
    name: id,
    prompt: '',
    schedule: { type: 'daily', time: '09:00' },
    workspace: '/tmp/w',
    mode: 'workspace',
    model: '',
    think: 'medium',
    notify: '',
    timeout: '15m',
    enabled: true,
    created_at: '',
    updated_at: '',
    last_run_at: '',
    last_status: '',
    next_run_at: '',
  });
  const run = (id: string): AutomationRun => ({
    id,
    task_id: 't-1',
    at: '',
    status: 'succeeded',
    error: '',
    conversation_id: '',
    run_id: '',
    duration_ms: 0,
    summary: '',
  });

  it('keeps only the newest task list', async () => {
    let releaseFirst: ((list: AutomationTask[]) => void) | undefined;
    const first = new Promise<AutomationTask[]>((resolve) => {
      releaseFirst = resolve;
    });
    apiMock.automations.mockReturnValueOnce(first);
    apiMock.automations.mockResolvedValueOnce([task('t-2')]);

    const older = useStore.getState().loadAutomations();
    const newer = useStore.getState().loadAutomations();
    await newer;
    releaseFirst?.([task('t-1')]);
    await older;

    // The stale answer describes the list as it was before the newer
    // refresh: a task that has just started would flip back to idle.
    expect(useStore.getState().automations.map((t) => t.id)).toEqual(['t-2']);
  });

  it('keeps only the newest run list of one task', async () => {
    let releaseFirst: ((list: AutomationRun[]) => void) | undefined;
    const first = new Promise<AutomationRun[]>((resolve) => {
      releaseFirst = resolve;
    });
    apiMock.automationRuns.mockReturnValueOnce(first);
    apiMock.automationRuns.mockResolvedValueOnce([run('run-2')]);

    const older = useStore.getState().loadAutomationRuns('t-1');
    const newer = useStore.getState().loadAutomationRuns('t-1');
    await newer;
    releaseFirst?.([run('run-1')]);
    await older;

    expect(
      (useStore.getState().automationRuns['t-1'] ?? []).map((r) => r.id),
    ).toEqual(['run-2']);
  });
});
