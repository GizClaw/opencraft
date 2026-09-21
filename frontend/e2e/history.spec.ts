import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { loadAllHistory } from './helpers';

function turns(n: number, prefix = 'message') {
  return Array.from({ length: n }, (_, i) => ({
    seq: i + 1,
    at: '2026-01-01T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: `${prefix}-${i}` }] },
      },
    ],
    artifacts: [],
  }));
}

test('resumes a long session with windowed transcript', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-old',
        title: 'Long session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 250,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(250),
  });
  await page.goto('/');

  // The sidebar lists the resumed session; click it to load history.
  await page.getByRole('button', { name: 'Long session' }).click();
  await expect(page.getByText('message-249')).toBeVisible();
  await expect(page.getByText('message-0')).not.toBeVisible();

  // Hydration stops at the newest page: reaching the top asks the
  // archive for one more page of older turns instead of the whole
  // session arriving up front.
  const scroller = page.getByTestId('chat-scroll');
  await scroller.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('message-234')).toBeVisible();
  await expect(page.getByText('message-0')).not.toBeVisible();

  // Keep reading upwards and the first turn eventually lands.
  await loadAllHistory(page);
  await expect(page.getByText('message-0')).toBeVisible();
});

test('opening a session from a transcript parked at the top lands on the newest message', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/Users/me/projects/opencraft',
    listSessions: [
      {
        id: 's-a',
        title: 'Session A',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 250,
        total_tokens: 0,
      },
      {
        id: 's-b',
        title: 'Session B',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        messages: 250,
        total_tokens: 0,
      },
    ],
    sessionTurnsByID: { 's-a': turns(250, 'a'), 's-b': turns(250, 'b') },
  });
  await page.goto('/');

  // Read session A from its top: the view unpins and offers the pill.
  await page.getByRole('button', { name: 'Session A' }).click();
  await expect(page.getByText('a-249', { exact: true })).toBeVisible();
  const scroller = page.getByTestId('chat-scroll');
  await scroller.evaluate((el) => el.scrollTo(0, 0));
  await expect(
    page.getByRole('button', { name: 'Jump to latest' }),
  ).toBeVisible();

  // The pin belongs to the transcript, not to the view: the session
  // opened next must follow its own newest message instead of landing on
  // the offset the previous transcript was parked at.
  await page.getByRole('button', { name: 'Session B' }).click();
  await expect(page.getByText('b-249', { exact: true })).toBeAttached();
  await expect
    .poll(() =>
      scroller.evaluate(
        (el) => el.scrollHeight - el.scrollTop - el.clientHeight,
      ),
    )
    .toBeLessThan(2);
  await expect(
    page.getByRole('button', { name: 'Jump to latest' }),
  ).toHaveCount(0);
});

test('collapsing a workspace folds an expanded session list back', async ({
  page,
}) => {
  const WS = '/Users/me/projects/opencraft';
  // Twelve sessions: the sidebar previews the ten newest and offers the
  // rest behind "More sessions".
  const sessions = Array.from({ length: 12 }, (_, i) => ({
    id: `s-${i}`,
    title: `session ${i}`,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    messages: 1,
    total_tokens: 0,
  }));
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: sessions,
  });
  await page.goto('/');

  const rows = page.locator('[data-session-id]');
  const more = page.getByTestId('more-sessions');
  const header = page.locator(`[data-tip="${WS}"]`);

  await expect(rows).toHaveCount(10);
  await expect(more).toBeVisible();

  await more.click();
  await expect(rows).toHaveCount(12);
  await expect(more).toHaveCount(0);

  // Folding the node folds the list too: the next expansion starts from
  // the preview window, with the offer to see the rest back on the table.
  await header.click();
  await expect(rows).toHaveCount(0);

  await header.click();
  await expect(rows).toHaveCount(10);
  await expect(more).toBeVisible();
});

// A turn the process never archived (the app was killed mid-reply) comes
// back as an interrupted one, and the only way to pick the work up is the
// transcript's own affordance: the engine does not replay the frontier, so
// "continue" sends the turn's message again as a fresh turn.
test('continues an interrupted turn with its own message', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-1',
        title: 'Crashed session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 2,
        total_tokens: 0,
      },
    ],
    sessionTurns: [
      {
        seq: 1,
        at: '2026-01-01T00:00:00Z',
        run_id: 'run-crashed',
        status: 'interrupted',
        interrupt_cause: 'app_restart',
        messages: [
          {
            role: 'user',
            content: {
              parts: [{ type: 'text', text: 'finish the report' }],
            },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'wrote the outline' }] },
          },
        ],
        artifacts: [],
      },
    ],
  });
  await page.goto('/');

  await page.getByRole('button', { name: 'Crashed session' }).click();
  await expect(page.getByText('Reply interrupted')).toBeVisible();
  await expect(
    page.getByText('The app closed or crashed while this reply was running.'),
  ).toBeVisible();

  await page.getByRole('button', { name: 'Continue' }).click();

  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as never as {
              __ocStartTurnCalls: { contextID: string; text: string }[];
            }
          ).__ocStartTurnCalls,
      ),
    )
    .toEqual([{ contextID: 's-1', text: 'finish the report' }]);
  // The carried-on turn is a normal turn: the message renders a second
  // time, after the interrupted reply it continues.
  await expect(
    page.getByTestId('chat-scroll').getByText('finish the report'),
  ).toHaveCount(2);
});
