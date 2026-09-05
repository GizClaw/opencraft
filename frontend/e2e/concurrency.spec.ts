import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

function streamEvent(conversationID: string, runID: string, text: string) {
  return {
    type: 'stream',
    data: {
      run_id: runID,
      conversation_id: conversationID,
      delta: {
        type: 'part',
        part: { type: 'text', text },
      },
    },
  };
}

function turnEnd(conversationID: string, runID: string) {
  return {
    type: 'turn_end',
    data: {
      run_id: runID,
      conversation_id: conversationID,
      status: 'completed',
    },
  };
}

function archivedTurn(runID: string, userText: string, assistantText: string) {
  return {
    seq: 1,
    at: '2026-09-04T00:00:00Z',
    run_id: runID,
    status: 'completed',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: userText }] },
      },
      {
        role: 'assistant',
        content: {
          parts: [{ type: 'text', text: assistantText }],
        },
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

function sessionRow(page: Page, id: string) {
  return page.locator(`[title="${id}"]`).getByRole('button').first();
}

test('runs two conversations concurrently and reconciles both from archive', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-active',
    newChatIds: ['s-bg'],
    startTurns: {
      's-active': { run_id: 'r-active' },
      's-bg': { run_id: 'r-bg' },
    },
    turnByRunID: {
      'r-active': archivedTurn(
        'r-active',
        'active prompt',
        'active archived final',
      ),
      'r-bg': archivedTurn('r-bg', 'background prompt', 'bg archived final'),
    },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'active prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText('active prompt')).toBeVisible();

  await page.getByRole('button', { name: 'New Chat' }).click();
  await typeComposerMessage(page, 'background prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText('background prompt')).toBeVisible();

  // The focused background conversation renders its streamed answer;
  // the active conversation's deltas are still folded into the store.
  await emit(page, streamEvent('s-bg', 'r-bg', 'bg partial answer'));
  await expect(page.getByText('bg partial answer')).toBeVisible();
  await emit(
    page,
    streamEvent('s-active', 'r-active', 'active partial answer'),
  );

  // Switch back to the first run while it is still streaming. Its
  // transcript must still be present because the run is live.
  await sessionRow(page, 's-active').click();
  await expect(page.getByText('active partial answer')).toBeVisible();

  // turn_end replaces both live transcripts with archive truth.
  await emit(page, turnEnd('s-active', 'r-active'));
  await expect(page.getByText('active archived final')).toBeVisible();
  await expect(page.getByText('active partial answer')).not.toBeVisible();

  await sessionRow(page, 's-bg').click();
  await expect(page.getByText('bg partial answer')).toBeVisible();
  await emit(page, turnEnd('s-bg', 'r-bg'));
  await expect(page.getByText('bg archived final')).toBeVisible();
  await expect(page.getByText('bg partial answer')).not.toBeVisible();
});

test('evicted idle conversations still resume from the archive', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-idle',
    listSessions: [
      {
        id: 's-idle',
        title: 'Idle session',
        created_at: '2026-09-04T00:00:00Z',
        updated_at: '2026-09-04T01:00:00Z',
        messages: 1,
        total_tokens: 0,
      },
    ],
    sessionTurnsByID: {
      's-idle': [
        {
          seq: 1,
          at: '2026-09-04T00:00:00Z',
          messages: [
            {
              role: 'user',
              content: { parts: [{ type: 'text', text: 'old question' }] },
            },
            {
              role: 'assistant',
              content: {
                parts: [{ type: 'text', text: 'old archived answer' }],
              },
            },
          ],
          artifacts: [],
        },
      ],
    },
  });
  await page.goto('/');
  await expect(page.getByText('old archived answer')).toBeVisible();

  // Switching to a fresh chat evicts the idle transcript; the sidebar
  // still lists the stored session for resume.
  await page.getByRole('button', { name: 'New Chat' }).click();
  await expect(
    page.getByRole('button', { name: 'Idle session' }),
  ).toBeVisible();

  await page.getByRole('button', { name: 'Idle session' }).click();
  await expect(page.getByText('old question')).toBeVisible();
  await expect(page.getByText('old archived answer')).toBeVisible();
});
