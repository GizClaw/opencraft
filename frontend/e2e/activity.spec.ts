import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

const WS = '/Users/me/projects/opencraft';

// emit pushes one UI event through the mock's dispatch hook, the same
// way the Go side streams a running turn.
async function emit(page: Page, data: unknown) {
  await page.evaluate(
    (d) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        d,
      ),
    data,
  );
}

async function startChat(page: Page) {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Ship the activity card');
  await page.getByRole('button', { name: 'Send' }).click();
}

// setProcesses replaces the mock's process feed answer: the card picks
// it up on its next read, which is what makes a growing tail and a
// process leaving the card assertable.
async function setProcesses(page: Page, rows: unknown[]) {
  await page.evaluate((r) => {
    const modules = (
      window as never as {
        __ocMockByModule: Record<string, Record<string, unknown>>;
      }
    ).__ocMockByModule;
    modules.Session.Processes = async () => r;
  }, rows);
}

function endTurn(page: Page) {
  return emit(page, {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
}

const reason = (text: string) => ({
  type: 'stream',
  data: {
    run_id: 'r-1',
    conversation_id: 's-1',
    delta: { type: 'part', part: { type: 'reasoning', text } },
  },
});

const say = (text: string) => ({
  type: 'stream',
  data: {
    run_id: 'r-1',
    conversation_id: 's-1',
    delta: { type: 'part', part: { type: 'text', text } },
  },
});

const running = {
  process_id: 'p-1',
  argv: ['npm', 'run', 'dev'],
  workdir: WS,
  tty: false,
  pid: 4242,
  started_at: '2026-01-01T00:00:00Z',
  running: true,
  tail: 'VITE v7.0.0  ready in 412 ms\n',
  truncated: false,
  seq: 30,
};

const exited = {
  ...running,
  running: false,
  exit_code: 0,
  exit_reason: 'exited',
  tail: `${running.tail}➜  Local:   http://localhost:5173/\n`,
  seq: 52,
};

// The card belongs to the conversation, not to the work: once it has
// something to report — a plan, a thought, a running process — it stays,
// and a turn ending does not take it down. What it never paints is a
// card with nothing to report, which is the only state it is absent in
// (and why it carries no close button: there is nothing to close).

test('shows the model thinking while it reasons, and keeps it after the turn', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Weighing the two layouts.'));
  await emit(page, reason(' The overlay wins.'));

  const section = page.getByTestId('think-section');
  await expect(section.getByText('Thinking…')).toBeVisible();
  await expect(page.getByTestId('think-body')).toHaveText(
    'Weighing the two layouts. The overlay wins.',
  );

  // The turn ends and the card stays where it stands: the thought is
  // still the newest thing the conversation has to report, and the card
  // is read beside the transcript, not instead of it.
  await endTurn(page);
  await expect(page.getByTestId('activity-card')).toBeVisible();
  await expect(page.getByTestId('think-section-header')).toHaveText('Thinking');
  await expect(page.getByTestId('think-body')).toHaveText(
    'Weighing the two layouts. The overlay wins.',
  );
});

test('paints nothing for a conversation with nothing to report', async ({
  page,
}) => {
  await startChat(page);
  // A turn that reasons nothing and plans nothing: there is no plan, no
  // thought and no process, so the card has nothing to hold and does not
  // paint an empty shell — a fresh conversation starts without it.
  await expect(page.getByTestId('activity-card')).toHaveCount(0);

  await endTurn(page);
  await expect(page.getByTestId('activity-card')).toHaveCount(0);
});

test('keeps the thought after the model moves on to the answer', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Reading the two error paths first.'));
  await expect(page.getByTestId('think-body')).toHaveText(
    'Reading the two error paths first.',
  );

  // The answer starts streaming: the block is done, and the card reports
  // that (the ellipsis and the spinner go) without folding the thought
  // away — it is the model's own account of the work, and the section
  // stays as the reader leaves it.
  await emit(page, say('The first path returns early.'));
  await expect(page.getByTestId('think-section-header')).toHaveText('Thinking');
  await expect(page.getByTestId('think-body')).toHaveText(
    'Reading the two error paths first.',
  );
  await expect(page.getByTestId('think-section-header')).toHaveAttribute(
    'aria-expanded',
    'true',
  );

  // And it is still the reader's to fold.
  await page.getByTestId('think-section-header').click();
  await expect(page.getByTestId('think-body')).toHaveCount(0);
});

// The card's one control sits at its top: the header folds the whole
// overlay down to itself, which is how a reader gets the corner back
// without losing the report that work is running (the dot goes on
// pulsing in the folded header).
test('folds to its header in one click, and unfolds the same way', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Watching the dev server.'));
  await setProcesses(page, [running]);

  const card = page.getByTestId('activity-card');
  const header = page.getByTestId('activity-card-header');
  const tail = page.getByTestId('process-tail');
  await expect(header).toHaveAttribute('aria-expanded', 'true');
  await expect(header).toHaveText('Activity');
  await expect(tail).toHaveText('VITE v7.0.0  ready in 412 ms');

  await header.click();
  await expect(card).toHaveAttribute('data-folded', 'true');
  await expect(page.getByTestId('think-body')).toHaveCount(0);
  await expect(page.getByTestId('process-section')).toHaveCount(0);
  // Folded, not dismissed: the header is still there and still live.
  await expect(header).toBeVisible();
  await expect(card).toHaveAttribute('data-live', 'true');

  // Work goes on behind the fold and nothing that arrives re-opens it —
  // the same rule the sections follow.
  await emit(page, reason(' Still here.'));
  await setProcesses(page, [
    { ...running, tail: 'VITE ready\nbuilding…\n', seq: 40 },
  ]);
  await expect(header).toHaveAttribute('aria-expanded', 'false');

  // Unfolding brings the body back carrying what happened while it was
  // folded.
  await header.click();
  await expect(header).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByTestId('think-body')).toHaveText(
    'Watching the dev server. Still here.',
  );
  await expect(tail).toHaveText('VITE ready\nbuilding…');
});

test('drops a stopped process while the card reports the rest of the turn', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Watching the dev server.'));
  await setProcesses(page, [running]);

  const card = page.getByTestId('activity-card');
  const tail = page.getByTestId('process-tail');
  await expect(card.getByText('npm run dev')).toBeVisible();
  await expect(card.getByText('running')).toBeVisible();
  await expect(tail).toHaveText('VITE v7.0.0  ready in 412 ms');

  // The next read brings the growing output.
  await setProcesses(page, [
    { ...running, tail: 'VITE ready\nbuilding…\n', seq: 40 },
  ]);
  await expect(tail).toHaveText('VITE ready\nbuilding…');

  // The dev server exits mid-turn. A stopped process is not activity: its
  // row — and with it the tail and the exit status — leaves at the next
  // read, while the card stays for the thought (and the turn).
  await setProcesses(page, [exited]);
  await expect(page.getByTestId('process-section')).toHaveCount(0);
  await expect(card.getByTestId('think-body')).toHaveText(
    'Watching the dev server.',
  );

  // The turn ends with nothing running: the row is gone, and the card
  // stays for the thought — nobody closed it.
  await endTurn(page);
  await expect(card).toBeVisible();
  await expect(card.getByTestId('think-body')).toHaveText(
    'Watching the dev server.',
  );
});

test('stays for a process that outlives its turn, until it stops', async ({
  page,
}) => {
  await startChat(page);
  await setProcesses(page, [running]);

  const card = page.getByTestId('activity-card');
  await expect(card.getByText('npm run dev')).toBeVisible();

  // The turn ends, the server it started does not. A running process is
  // activity in its own right — that is why the feed keeps polling it at
  // the idle pace — so the card is still there right after the turn,
  // where a stopped process's card left with it (see above).
  await endTurn(page);
  await expect(card).toBeVisible();

  // The server keeps printing; the next poll brings the newer tail. That
  // pace is the idle one (5s), which is longer than the default budget.
  await setProcesses(page, [
    { ...running, tail: 'VITE ready\nhmr update /src/App.tsx\n', seq: 74 },
  ]);
  await expect(page.getByTestId('process-tail')).toHaveText(
    'VITE ready\nhmr update /src/App.tsx',
    { timeout: 15_000 },
  );

  // Then the server exits: the card is left with nothing to report (no
  // plan, no thought, no process), which is the one state it does not
  // paint in.
  await setProcesses(page, [exited]);
  await expect(card).toHaveCount(0, { timeout: 15_000 });
});

test('streams a thought on its own cadence, not per token', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Thinking.'));
  await expect(page.getByTestId('think-body')).toHaveText('Thinking.');

  // Sample what the reader sees: the visible thought block changing from
  // one animation frame to the next is one update. Sampling per frame
  // also caps what a per-token regression could ever show.
  await page.evaluate(() => {
    const state = { changes: 0, last: '' };
    (window as never as { __ocThinkUpdates: typeof state }).__ocThinkUpdates =
      state;
    const tick = () => {
      const text =
        document.querySelector('[data-testid="think-body"]')?.textContent ?? '';
      if (text !== '' && text !== state.last) {
        if (state.last !== '') state.changes += 1;
        state.last = text;
      }
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });

  // 200 tokens over ~1.6s, the pace a fast model reasons at.
  await page.evaluate(async () => {
    await new Promise<void>((done) => {
      const emit = (
        window as never as { __emit: (n: string, v: unknown) => void }
      ).__emit;
      let sent = 0;
      const timer = setInterval(() => {
        emit('opencraft:ui', {
          type: 'stream',
          data: {
            run_id: 'r-1',
            conversation_id: 's-1',
            delta: {
              type: 'part',
              part: { type: 'reasoning', text: 'reasoning token ' },
            },
          },
        });
        sent += 1;
        if (sent >= 200) {
          clearInterval(timer);
          done();
        }
      }, 8);
    });
  });
  await page.waitForTimeout(600);

  const state = await page.evaluate(
    () =>
      (window as never as { __ocThinkUpdates: { changes: number } })
        .__ocThinkUpdates,
  );
  // Every token is in the block…
  await expect(page.getByTestId('think-body')).toContainText(
    'reasoning token reasoning token reasoning token',
  );
  // …but the block was committed a handful of times, not 200: the
  // reasoning cadence is 250ms, and per-token commits would change the
  // text on nearly every frame (up to ~96 over this burst).
  expect(state.changes).toBeGreaterThanOrEqual(2);
  expect(state.changes).toBeLessThanOrEqual(30);
});

// A card update is the card's own business. The thought tail re-pins its
// scroller as text streams, and that inner scroll used to be read as "the
// page moved", which dismissed any menu open anywhere in the app — a
// settings dropdown, in this case, closed under the reader's cursor while
// the model kept thinking.
test('does not dismiss a menu open elsewhere while streaming', async ({
  page,
}) => {
  await startChat(page);
  await emit(page, reason('Thinking.'));

  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Interface' }).click();
  await page.getByRole('button', { name: 'English', exact: true }).click();
  const option = page.getByRole('menuitem', { name: '中文' });
  await expect(option).toBeVisible();

  // Enough thought to overflow the tail, so the section re-pins itself
  // the way it does through a long reasoning phase.
  for (let i = 0; i < 60; i += 1) {
    await emit(page, reason('reasoning chunk '));
  }
  const tail = page.getByTestId('think-body');
  // The queue commits on its own beat, so wait for the block to carry
  // the thought and for the re-pin that follows it.
  await expect(tail).toContainText('reasoning chunk reasoning chunk');
  await expect.poll(() => tail.evaluate((el) => el.scrollTop > 0)).toBe(true);

  // And nothing moved focus: the card's controls (the fold header, a
  // process row) take focus only when they are clicked, and nothing in
  // the card ever calls focus() on its own, so the caret stays on the
  // control the reader clicked.
  expect(
    await page.evaluate(() => document.activeElement?.textContent ?? ''),
  ).toBe('English');
  await expect(option).toBeVisible();
});

// And the same rule for a hover hint: the card re-pins its tail as the
// model thinks, which is a scroll in a pane the composer is not in, so a
// hint resting on a control down there stays put.
test('keeps a hint on the composer while the card scrolls', async ({
  page,
}) => {
  await startChat(page);
  await page.getByRole('button', { name: 'Stop' }).hover();
  const hint = page.getByRole('tooltip');
  await expect(hint).toHaveText('Stop');

  // Enough thought to overflow the tail, so the section re-pins itself
  // the way it does through a long reasoning phase.
  for (let i = 0; i < 60; i += 1) {
    await emit(page, reason('reasoning chunk '));
  }
  const tail = page.getByTestId('think-body');
  await expect.poll(() => tail.evaluate((el) => el.scrollTop > 0)).toBe(true);

  await expect(hint).toBeVisible();
});
