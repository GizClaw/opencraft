import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// Whether a waiting row actually pulses is a stylesheet question, and
// jsdom never resolves one: vitest asserts the class and the icon, and
// the animation is checked here, against the built app.

function emit(page: Page, data: unknown) {
  return page.evaluate(
    (payload) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        payload,
      ),
    data,
  );
}

function meta(id: string, title: string) {
  return {
    id,
    title,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    messages: 2,
    total_tokens: 0,
  };
}

const row = (page: Page, id: string) =>
  page.locator(`[data-session-id="${id}"]`);

const animationName = (page: Page, id: string) =>
  row(page, id).evaluate((el) => getComputedStyle(el).animationName);

test('a prompt waiting on the reader pulses its row until it is answered', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    listSessions: [meta('s-1', 'the session')],
  });
  await page.goto('/');

  await typeComposerMessage(page, 'do the thing');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(row(page, 's-1').locator('svg.animate-spin')).toBeVisible();
  // Work in progress is not a request: a spinning row stays still.
  expect(await animationName(page, 's-1')).toBe('none');

  // The run stops to ask something the reader has to answer.
  await emit(page, {
    type: 'interact',
    data: {
      id: 'p-1',
      run_id: 'r-1',
      conversation_id: 's-1',
      kind: 'confirm',
      severity: 'notice',
      title: 'Allow the write?',
      body: [],
      options: [],
      multi: false,
      allow_other: false,
      source: 'test',
    },
  });
  await expect(
    row(page, 's-1').locator('[data-tip="Waiting for you"]'),
  ).toBeVisible();
  await expect
    .poll(() => animationName(page, 's-1'))
    .toBe('sidebar-row-attention');
  // The question mark is the icon now: the row is not still working.
  await expect(row(page, 's-1').locator('svg.animate-spin')).toHaveCount(0);

  await emit(page, {
    type: 'resolved',
    data: { id: 'p-1', conversation_id: 's-1' },
  });
  await expect(
    row(page, 's-1').locator('[data-tip="Waiting for you"]'),
  ).toHaveCount(0);
  await expect(row(page, 's-1').locator('svg.animate-spin')).toBeVisible();
  expect(await animationName(page, 's-1')).toBe('none');
});
