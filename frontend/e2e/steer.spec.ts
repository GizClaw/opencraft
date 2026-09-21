// Mid-turn steering: a message submitted while a turn is running is handed
// to the live run (Enter) instead of waiting for a turn of its own, and the
// transcript keeps the text when the run ends without the engine draining it.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// A run's archived copy: the user rows the archive holds and the answer. A
// delivered steer is in here (it entered the conversation); an undelivered
// one never is, which is what makes the card the only copy of that text.
function archivedTurn(runID: string, rows: string[], answer: string) {
  return {
    seq: 1,
    at: '2026-09-04T00:00:00Z',
    run_id: runID,
    status: 'completed',
    messages: [
      ...rows.map((text) => ({
        role: 'user',
        content: { parts: [{ type: 'text', text }] },
      })),
      {
        role: 'assistant',
        content: { parts: [{ type: 'text', text: answer }] },
      },
    ],
    artifacts: [],
  };
}

async function emit(page: Page, data: unknown) {
  await page.evaluate(
    (payload) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        payload,
      ),
    data,
  );
}

async function steerCalls(page: Page) {
  return page.evaluate(
    () =>
      (
        window as never as {
          __ocSteerCalls: { runID: string; text: string }[];
        }
      ).__ocSteerCalls ?? [],
  );
}

/** The transcript row carrying a text, ignoring any other copy of it. */
function rowWith(page: Page, text: string) {
  return page.locator('[data-msg-index]').filter({ hasText: text });
}

async function startTurn(page: Page, opts: { archive?: string[] }) {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    turnByRunID: {
      'r-1': archivedTurn('r-1', opts.archive ?? ['first prompt'], 'done'),
    },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(rowWith(page, 'first prompt')).toBeVisible();
}

test('hands a mid-turn message to the live run', async ({ page }) => {
  await startTurn(page, { archive: ['first prompt', 'mid-turn note'] });

  await typeComposerMessage(page, 'mid-turn note');
  await page.keyboard.press('Enter');

  // The RPC carries the live run's id: the composer handed the text to the
  // running turn rather than staging it behind it as a Tab draft.
  await expect
    .poll(() => steerCalls(page))
    .toEqual([{ runID: 'r-1', text: 'mid-turn note' }]);
  await expect(rowWith(page, 'mid-turn note')).toBeVisible();

  // A zero count is a real zero: the run delivered the message, so it ends
  // up in the archive, and reconciliation replaces the optimistic row with
  // the archived one. One row, not two, and no undelivered card.
  await emit(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'completed',
      steer_pending: 0,
    },
  });
  await expect(rowWith(page, 'mid-turn note')).toHaveCount(1);
  await expect(page.getByTestId('steer-undelivered')).toHaveCount(0);
});

test('keeps the text when the run ends without the count', async ({ page }) => {
  // The archive never saw the steered text, so if the turn_end loses it the
  // text is gone for good — which is exactly what an unreadable count must
  // not do. A producer that cannot read the count leaves the field out (or
  // sends null), and the UI keeps every steered row as a card instead of
  // reading that as "everything made it".
  await startTurn(page, { archive: ['first prompt'] });

  await typeComposerMessage(page, 'mid-turn note');
  await page.keyboard.press('Enter');
  await expect(rowWith(page, 'mid-turn note')).toBeVisible();

  await emit(page, {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });

  const card = page.getByTestId('steer-undelivered');
  await expect(card).toContainText('mid-turn note');
  // The row moves into the card: the text is kept, not duplicated.
  await expect(rowWith(page, 'mid-turn note')).toHaveCount(0);
});
