import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// The sidebar's two attention states are asserted in vitest at the level
// a unit test can see: a class on the row, an icon in it. Whether that
// class actually pulses the row is a stylesheet question, and jsdom never
// resolves one - so the animation is checked here, against the built app.

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

test('a turn that ends on its own leaves a mark the next ending clears', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    listSessions: [meta('s-1', 'the session')],
  });
  await page.goto('/');
  const mark = row(page, 's-1').locator('[data-tip="Turn failed"]');

  await typeComposerMessage(page, 'do the thing');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: 'working' } },
    },
  });
  await emit(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'failed',
      error: 'provider 500',
      error_kind: 'provider_failure',
    },
  });
  await expect(mark).toBeVisible();
  expect(await animationName(page, 's-1')).toBe('none');

  // A deadline shares the wire status with a user stop and is the one
  // that still needs the reader: the row marks it too. Nothing marks
  // this one live - the store reads the event, not the turn - which is
  // what keeps a run in another workspace visible here.
  await emit(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-2',
      conversation_id: 's-1',
      status: 'canceled',
      error_kind: 'timeout',
    },
  });
  await expect(mark).toBeVisible();

  // The reader's own Stop is not news: the last ending says so, and the
  // row goes quiet.
  await emit(page, {
    type: 'turn_end',
    data: { run_id: 'r-3', conversation_id: 's-1', status: 'canceled' },
  });
  await expect(mark).toHaveCount(0);
});

test('a marked row opens, and the mark goes with the click', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurns: { 's-2': { run_id: 'r-2' } },
    listSessions: [meta('s-1', 'the session'), meta('s-2', 'the other one')],
  });
  await page.goto('/');

  // A turn that failed while this window was elsewhere: the event alone
  // puts the mark on that row.
  await emit(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-9',
      conversation_id: 's-2',
      status: 'failed',
      error: 'boom',
      error_kind: 'provider_failure',
    },
  });
  const mark = row(page, 's-2').locator('[data-tip="Turn failed"]');
  await expect(mark).toBeVisible();

  await page.getByRole('button', { name: 'the other one' }).click();

  // Opening the row is the reader answering it: the mark is gone, and
  // the conversation is on screen.
  await expect(mark).toHaveCount(0);
  await expect(row(page, 's-2')).toHaveAttribute('data-session-id', 's-2');
});
