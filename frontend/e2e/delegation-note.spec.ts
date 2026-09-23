// A delegated subagent reports back as a note turn of the conversation
// that asked for it. Nothing streams it — the note is appended to the
// archive when the child finishes, which can be long after the turn that
// spawned it ended — so a conversation that is already open has to notice
// the tail of its own archive and draw the card itself.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

const user = (text: string) => ({
  role: 'user',
  content: { parts: [{ type: 'text', text }] },
});
const assistant = (text: string) => ({
  role: 'assistant',
  content: { parts: [{ type: 'text', text }] },
});

// The turn the user wrote.
const asked = {
  seq: 1,
  at: '2026-09-04T00:00:00Z',
  run_id: 'r-1',
  status: 'completed',
  messages: [
    user('research the beta customers'),
    assistant('spawned researcher'),
  ],
  artifacts: [],
};

// The note the app wrote when the subagent finished. Its row is a
// user-role message carrying the rendered prose (that is what the model
// reads next turn); the card renders from the decoded fields beside it.
const note = (seq = 2) => ({
  seq,
  at: '2026-09-04T00:05:00Z',
  run_id: 'subagent:card-7',
  status: 'completed',
  kind: 'delegation_note',
  delegation_note: {
    target: 'researcher',
    status: 'succeeded',
    card_id: 'card-7',
    body: 'the report body',
  },
  messages: [
    user(
      '[delegated worker "researcher" finished: succeeded]\n\nthe report body',
    ),
  ],
  artifacts: [],
});

async function emitSessionUpdated(page: Page, id: string) {
  await page.evaluate(
    (conversationID) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        { type: 'session_updated', data: { id: conversationID } },
      ),
    id,
  );
}

test('draws a note the archive appended on its own in the open transcript', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    sessionTurns: [asked],
    turnsSince: [note()],
  });
  await page.goto('/');
  await expect(page.getByText('spawned researcher')).toBeVisible();

  // The subagent finishes: the backend appends the note turn and signals
  // the session. No reload, no session switch — the open transcript picks
  // up the tail of its archive.
  await emitSessionUpdated(page, 's-1');

  const card = page.getByTestId('delegation-note');
  await expect(card).toBeVisible();
  await expect(card).toContainText('the report body');
  // A tail append, not a rebuild: what was on screen is still there, and
  // the note is the row below it.
  await expect(page.getByText('spawned researcher')).toBeVisible();
  const rows = page.locator('[data-msg-index]');
  await expect(rows).toHaveCount(3);
  // The card renders from the archived fields, and it is the last row of
  // the transcript.
  await expect(rows.last()).toHaveAttribute('data-testid', 'delegation-note');
});

test('holds a note that landed mid-turn until the turn ends', async ({
  page,
}) => {
  const followUp = {
    // The turn's own row is written when it ends, so the note written
    // while it ran sits one seq below it.
    seq: 3,
    at: '2026-09-04T00:02:00Z',
    run_id: 'r-2',
    status: 'completed',
    messages: [user('follow up'), assistant('the answer')],
    artifacts: [],
  };
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-2', context_id: 's-1' },
    sessionTurns: [asked],
    turnByRunID: { 'r-2': followUp },
    turnsSince: [note(2), followUp],
  });
  await page.goto('/');
  await expect(page.getByText('spawned researcher')).toBeVisible();

  await typeComposerMessage(page, 'follow up');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText('follow up')).toBeVisible();

  // Mid-turn the answer is still streaming into the last row: folding the
  // note in now would split the answer around it.
  await emitSessionUpdated(page, 's-1');
  await page.waitForTimeout(200);
  await expect(page.getByTestId('delegation-note')).toHaveCount(0);

  // The turn ends and the note takes its place above the finished turn —
  // where a full hydrate renders it — without printing that turn twice.
  await page.evaluate(() =>
    (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
      'opencraft:ui',
      {
        type: 'turn_end',
        data: {
          run_id: 'r-2',
          conversation_id: 's-1',
          status: 'completed',
          steer_pending: 0,
        },
      },
    ),
  );

  const card = page.getByTestId('delegation-note');
  await expect(card).toBeVisible();
  await expect(card).toContainText('the report body');
  await expect(page.getByText('the answer')).toHaveCount(1);
  // The card sits above the turn it was written during, which is where a
  // full hydrate renders it — and the finished turn is not printed twice.
  const rows = page.locator('[data-msg-index]');
  await expect(rows).toHaveCount(5);
  await expect(rows.nth(2)).toHaveAttribute('data-testid', 'delegation-note');
  await expect(rows.nth(3)).toContainText('follow up');
  await expect(rows.nth(4)).toContainText('the answer');
});
