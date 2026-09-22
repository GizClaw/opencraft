// Mid-turn steering: a message submitted while a turn is running is handed
// to the live run (Enter) instead of waiting for a turn of its own, it is
// drawn as an interjection in the position it was typed, and the transcript
// keeps that text when the run ends without the engine draining it.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// A run's archived copy: the user rows the archive holds and the answer. A
// delivered steer is in here (it entered the conversation); an undelivered
// one never is, which is what makes the note the only copy of that text.
//
// The rows are wired the way the engine archives them: the ask, the tool
// round whose result is the boundary a steer is appended at, the steered
// rows themselves, then the answer. A mid-turn user row in an archive only
// ever comes from that node, which is why it is the one shape the resume
// reads as an interjection.
function archivedTurn(runID: string, rows: string[], answer: string) {
  const [ask, ...steers] = rows;
  const user = (text: string) => ({
    role: 'user',
    content: { parts: [{ type: 'text', text }] },
  });
  return {
    seq: 1,
    at: '2026-09-04T00:00:00Z',
    run_id: runID,
    status: 'completed',
    messages: [
      user(ask),
      {
        role: 'assistant',
        content: {
          parts: [
            { type: 'tool_call', call: { id: 'c-1', name: 'read_file' } },
          ],
        },
      },
      {
        role: 'tool',
        content: {
          parts: [
            {
              type: 'tool_result',
              result: {
                call_id: 'c-1',
                content: [{ type: 'text', text: 'ok' }],
              },
            },
          ],
        },
      },
      ...steers.map(user),
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
  // The row is an interjection from the first paint, and it says it is
  // still waiting for the step that reads it.
  const note = page.getByTestId('steer-note');
  await expect(note).toHaveAttribute('data-steer-state', 'pending');
  await expect(note).toContainText('mid-turn note');

  // A zero count is a real zero: the run delivered the message, so it ends
  // up in the archive, and reconciliation replaces the optimistic row with
  // the archived one. One row, not two, and it settles as delivered.
  await emit(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'completed',
      steer_pending: 0,
    },
  });
  await expect(note).toHaveAttribute('data-steer-state', 'delivered');
  await expect(rowWith(page, 'mid-turn note')).toHaveCount(1);
  // Delivered means the conversation owns the text now: nothing to resend.
  await expect(
    page.getByRole('button', { name: 'Send as a new turn' }),
  ).toHaveCount(0);
});

test('settles the note while the turn is still running', async ({ page }) => {
  await startTurn(page, { archive: ['first prompt'] });

  await typeComposerMessage(page, 'mid-turn note');
  await page.keyboard.press('Enter');
  const note = page.getByTestId('steer-note');
  await expect(note).toHaveAttribute('data-steer-state', 'pending');

  // The boundary that read the queue says so right away: the note stops
  // claiming it is waiting for a step that already took it, and it does
  // that mid-turn — not only once the whole turn is over, which would
  // leave a reader of a long turn staring at a stale "waiting".
  await emit(page, {
    type: 'steer_pending',
    data: { run_id: 'r-1', conversation_id: 's-1', steer_pending: 0 },
  });
  await expect(note).toHaveAttribute('data-steer-state', 'delivered');
  await expect(note).not.toContainText('waiting for the next step');
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

  // The note stays exactly where the words were typed — after the answer it
  // missed — and it offers the ways out of a text that never made it: send
  // it as a turn of its own, or drop it.
  const note = page.getByTestId('steer-note');
  await expect(note).toHaveAttribute('data-steer-state', 'undelivered');
  await expect(note).toContainText('mid-turn note');
  await expect(rowWith(page, 'mid-turn note')).toHaveCount(1);
  await expect(
    note.getByRole('button', { name: 'Send as a new turn' }),
  ).toBeVisible();
});
