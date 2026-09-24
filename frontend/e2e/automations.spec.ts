import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

const user = (text: string) => ({
  role: 'user',
  content: { parts: [{ type: 'text', text }] },
});
const assistant = (text: string) => ({
  role: 'assistant',
  content: { parts: [{ type: 'text', text }] },
});

// The turn already in the archive when the scheduler fires.
const asked = {
  seq: 1,
  at: '2026-09-04T00:00:00Z',
  run_id: 'r-1',
  status: 'completed',
  messages: [user('ask'), assistant('answered')],
  artifacts: [],
};

function emitEvent(page: Page, data: unknown) {
  return page.evaluate(
    (d) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        d,
      ),
    data,
  );
}

// A scheduled occurrence is the one run the UI never starts itself, so
// nothing opens a turn strip for it: without one, the files it writes
// while you watch have no strip to land on and only appear once the turn
// folds in from the archive — the live/reload split, pointing the other
// way. The strip, its files and its terminal state all come from the
// run's own events now.
test('a scheduled run on the open conversation draws its own strip', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    sessionTurns: [asked],
    // The archived copy of the scheduled turn, which the app fetches by
    // run id when the run ends: it has to replace the live rows, not sit
    // beside them.
    turnByRunID: {
      'r-auto': {
        seq: 2,
        at: '2026-09-04T00:10:00Z',
        run_id: 'r-auto',
        status: 'completed',
        duration_ms: 1500,
        messages: [user('write the brief'), assistant('wrote it')],
        artifacts: [{ path: 'reports/brief.md', bytes: 42 }],
      },
    },
  });
  await page.goto('/');
  await expect(page.getByText('answered')).toBeVisible();

  await emitEvent(page, {
    type: 'automation_run_started',
    data: {
      run_id: 'r-auto',
      conversation_id: 's-1',
      message: 'write the brief',
    },
  });
  await emitEvent(page, {
    type: 'stream',
    data: {
      run_id: 'r-auto',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: { type: 'text', text: 'writing the brief' },
      },
    },
  });
  await emitEvent(page, {
    type: 'artifact',
    data: {
      conversation_id: 's-1',
      run_id: 'r-auto',
      path: 'reports/brief.md',
      bytes: 42,
    },
  });

  // The run opens its own turn: the task's message as the user row, the
  // streamed answer under it, and the strip holding the file.
  await expect(page.getByText('write the brief')).toBeVisible();
  await expect(page.getByText('writing the brief')).toBeVisible();
  // The file is drawn on the scheduled run's own strip, not on the turn
  // above it.
  await expect(page.getByRole('button', { name: 'brief.md' })).toBeVisible();

  await emitEvent(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-auto',
      conversation_id: 's-1',
      status: 'completed',
      duration_ms: 1500,
    },
  });

  // The archive's copy of the turn replaces the live one: the drawn row
  // is the one a reload shows, and the file is still on this turn's
  // strip.
  await expect(page.getByText('wrote it')).toBeVisible();
  await expect(page.getByText('writing the brief')).toHaveCount(0);
  await expect(page.getByText('write the brief')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'brief.md' })).toBeVisible();
});

test('automations view lists configured tasks', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    automations: [
      {
        id: 't-1',
        name: 'Daily brief',
        prompt: 'summarize',
        schedule: { type: 'daily', time: '09:00' },
        workspace: '/workspace',
        mode: 'workspace',
        model: '',
        think: 'medium',
        conversation_id: '',
        notify: 'always',
        enabled: true,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        last_run_at: '',
        last_status: '',
        next_run_at: '2026-01-02T09:00:00Z',
      },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Automations' }).click();
  await expect(page.getByText('Daily brief')).toBeVisible();
});

test('the split button keeps both halves on one height', async ({ page }) => {
  await page.addInitScript(mockBackend as never, { automations: [] });
  await page.goto('/');
  await page.getByRole('button', { name: 'Automations' }).click();
  const create = page.getByRole('button', { name: 'New task' }).first();
  await create.waitFor();
  // The caret used to be `h-full` inside an auto-height flex row, which
  // resolves to `auto`: the icon half settled at its content height and
  // hovered as a shorter inset slab next to the button.
  const [a, b] = await Promise.all([
    create.boundingBox(),
    create.evaluate((node) =>
      (node.nextElementSibling as HTMLElement).getBoundingClientRect().toJSON(),
    ),
  ]);
  expect(a).not.toBeNull();
  expect(b).not.toBeNull();
  expect(Math.abs(a!.height - b!.height)).toBeLessThan(0.5);
  expect(Math.abs(a!.y - b!.y)).toBeLessThan(0.5);
  expect(b!.x).toBeCloseTo(a!.x + a!.width, 1);
});

// A live run offers the stop in its history row, and the click has to
// reach Automation.CancelRun with that run's id: the call log is the
// only proof the button is wired to the binding rather than to a local
// state change.
test('a live run can be stopped from its history row', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    automations: [
      {
        id: 't-1',
        name: 'Daily brief',
        prompt: 'summarize',
        schedule: { type: 'daily', time: '09:00' },
        workspace: '/workspace',
        mode: 'workspace',
        model: '',
        think: 'medium',
        conversation_id: '',
        notify: 'always',
        enabled: true,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        last_run_at: '',
        last_status: '',
        next_run_at: '2026-01-02T09:00:00Z',
      },
    ],
    handlers: handlerSources({
      'Automation.Runs': async () => [
        {
          id: 'run_1',
          task_id: 't-1',
          at: '2026-09-21T09:00:00Z',
          status: 'running',
          error: '',
          conversation_id: 's-1',
          run_id: 'r-1',
          duration_ms: 0,
          summary: '',
        },
      ],
      'Automation.CancelRun': async (runID: string) => {
        const w = window as unknown as { __ocCancelCalls?: string[] };
        w.__ocCancelCalls = [...(w.__ocCancelCalls ?? []), runID];
      },
    }),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Automations' }).click();
  await page.getByText('Daily brief').click();
  await page.getByRole('button', { name: 'Cancel run' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as unknown as { __ocCancelCalls?: string[] }).__ocCancelCalls,
      ),
    )
    .toEqual(['run_1']);
});
