import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// F-09 and F-10 end to end: the real store, the real components and the
// mock backend's event channel. The vitest layer pins the actor's own
// states; what these two cover is the wire shape that reaches it - a run
// that begins after the previous turn has settled, and a terminal event
// whose status is outside the set the client can render.

function archivedTurn(
  runID: string,
  seq: number,
  userText: string,
  assistantText: string,
  artifacts: { path: string; bytes: number }[] = [],
) {
  return {
    seq,
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
        content: { parts: [{ type: 'text', text: assistantText }] },
      },
    ],
    artifacts,
  };
}

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

const streamEvent = (conversationID: string, runID: string, text: string) => ({
  type: 'stream',
  data: {
    run_id: runID,
    conversation_id: conversationID,
    delta: { type: 'part', part: { type: 'text', text } },
  },
});

const turnEnd = (
  conversationID: string,
  runID: string,
  status = 'completed',
  extra: Record<string, unknown> = {},
) => ({
  type: 'turn_end',
  data: { run_id: runID, conversation_id: conversationID, status, ...extra },
});

const artifactEvent = (
  conversationID: string,
  runID: string,
  path: string,
  bytes: number,
) => ({
  type: 'artifact',
  data: { run_id: runID, conversation_id: conversationID, path, bytes },
});

test('a run that starts after the previous turn ended keeps its own live strip', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    turnByRunID: {
      'r-1': archivedTurn('r-1', 1, 'first prompt', 'first archived answer', [
        { path: '/tmp/w/first-file.md', bytes: 12 },
      ]),
      'r-2': archivedTurn(
        'r-2',
        2,
        'automation prompt',
        'second archived answer',
      ),
    },
  });
  await page.goto('/');

  // Turn one is this window's own send, and it settles before anything
  // else happens: the conversation rests on the ending.
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, streamEvent('s-1', 'r-1', 'first live answer'));
  await expect(page.getByText('first live answer')).toBeVisible();
  await emit(page, turnEnd('s-1', 'r-1'));
  await expect(page.getByText('first archived answer')).toBeVisible();
  await expect(page.getByText('first-file.md')).toBeVisible();

  // A scheduled automation pinned to the same conversation starts its
  // own run while the turn rests on `succeeded`: the window never sent
  // it and holds no strip for it.
  await emit(page, {
    type: 'automation_run_started',
    data: { run_id: 'r-2', conversation_id: 's-1', message: 'write the brief' },
  });
  await expect(page.getByTestId('chat-run-state')).toBeVisible();

  await emit(page, streamEvent('s-1', 'r-2', 'second live answer'));
  await expect(page.getByText('second live answer')).toBeVisible();
  await emit(page, artifactEvent('s-1', 'r-2', '/tmp/w/report.md', 24));
  await expect(page.getByText('report.md')).toBeVisible();

  // The file belongs to r-2's own strip, not to the finished turn above
  // it: two artifact strips, one file each.
  const reportStrip = page
    .getByRole('button', { name: 'report.md' })
    .locator('xpath=ancestor::div[contains(@class,"rounded-card")][1]');
  // The ancestor has to resolve first: a renamed container would make
  // the child locator match nothing, and the count below would pass
  // without testing anything.
  await expect(reportStrip).toHaveCount(1);
  await expect(reportStrip.getByText('first-file.md')).toHaveCount(0);
  const firstStrip = page
    .getByRole('button', { name: 'first-file.md' })
    .locator('xpath=ancestor::div[contains(@class,"rounded-card")][1]');
  await expect(firstStrip).toHaveCount(1);
  await expect(firstStrip.getByText('report.md')).toHaveCount(0);

  // Its own ending reconciles from the archive like any other turn.
  await emit(page, turnEnd('s-1', 'r-2'));
  await expect(page.getByText('second archived answer')).toBeVisible();
  await expect(page.getByText('second live answer')).not.toBeVisible();

  // A delta of the run that ended before r-2 ever started is the one
  // genuinely stale shape the gate exists for: one ended-run slot no
  // longer remembers r-1 now that r-2 was the last to end, and taking
  // it for a live run would pin the conversation to a run whose
  // terminal event it already consumed. A third run follows it, and its
  // own delta is the barrier: once that text is on screen the flush has
  // been processed, so the stale one must not be.
  await emit(page, streamEvent('s-1', 'r-1', 'stale first-run tail'));
  await emit(page, {
    type: 'automation_run_started',
    data: {
      run_id: 'r-3',
      conversation_id: 's-1',
      message: 'scheduled prompt',
    },
  });
  await emit(page, streamEvent('s-1', 'r-3', 'third live answer'));
  await expect(page.getByText('third live answer')).toBeVisible();
  await expect(page.getByText('stale first-run tail')).not.toBeVisible();
});

test('a terminal status the client cannot name still ends the turn', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'hello');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, streamEvent('s-1', 'r-1', 'partial answer'));
  await expect(page.getByTestId('chat-run-state')).toBeVisible();

  // "unknown" is the status a wait that produced no result used to
  // publish: no consumer can render it, and the error kind is the one
  // thing that separates a deadline from a user stop. The turn has to
  // settle as the failure it is, with that reason attached.
  await emit(
    page,
    turnEnd('s-1', 'r-1', 'unknown', {
      error: 'context deadline exceeded',
      error_kind: 'timeout',
    }),
  );

  await expect(page.getByTestId('chat-run-state')).not.toBeVisible();
  const stop = page.getByTestId('chat-turn-stop');
  await expect(stop).toBeVisible();
  await expect(stop).toHaveAttribute('data-status', 'failed');
  await expect(stop).toHaveAttribute('data-tip', 'context deadline exceeded');
});

test('a failed send cannot reopen the run that ended before it', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    turnByRunID: {
      'r-1': archivedTurn('r-1', 1, 'first prompt', 'first archived answer'),
    },
    // The first send starts a run and the second one fails before it
    // ever had one; the third send is how the test proves the failure
    // left the conversation able to start a run again. The handler is
    // recompiled inside the page, so it keeps its call count on
    // `window`.
    handlers: handlerSources({
      'Conversation.StartTurn': async function () {
        const w = window as never as { __startCalls?: number };
        w.__startCalls = (w.__startCalls ?? 0) + 1;
        if (w.__startCalls === 2) throw new Error('start boom');
        return { run_id: `r-${w.__startCalls}`, context_id: 's-1' };
      },
    }),
  });
  await page.goto('/');

  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, streamEvent('s-1', 'r-1', 'first live answer'));
  await expect(page.getByText('first live answer')).toBeVisible();
  await emit(page, turnEnd('s-1', 'r-1'));
  await expect(page.getByText('first archived answer')).toBeVisible();

  // The next send never produces a run, and its failure must leave the
  // ended-run memory alone: the catch path used to synthesize a
  // terminal event with an empty run id, which wiped the only slot.
  await typeComposerMessage(page, 'second prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByTestId('chat-turn-stop')).toBeVisible();

  await emit(page, streamEvent('s-1', 'r-1', 'late first-run tail'));

  // The stale delta must not turn the failed turn back into a live one:
  // the composer still offers a send, that send starts a run of its
  // own, and only then - with the flush long since processed - is the
  // stale text's absence worth asserting.
  await typeComposerMessage(page, 'third prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, streamEvent('s-1', 'r-3', 'third live answer'));
  await expect(page.getByText('third live answer')).toBeVisible();
  await expect(page.getByTestId('chat-run-state')).toBeVisible();
  await expect(page.getByText('late first-run tail')).not.toBeVisible();
});
