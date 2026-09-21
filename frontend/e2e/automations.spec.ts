import { expect, test } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

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
