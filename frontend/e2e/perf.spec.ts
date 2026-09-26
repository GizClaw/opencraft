import { expect, test, type Locator } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { loadAllHistory } from './helpers';

function turns(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    seq: i + 1,
    at: '2026-01-01T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: `message-${i}` }] },
      },
    ],
    artifacts: [],
  }));
}

// turnsOf builds `count` turns of `each` messages: the shape a working
// session has, where one turn is a whole tool-using exchange rather than a
// single line. Hydration keeps the newest six turns, so this is also what
// decides how many messages are loaded when the session opens.
function turnsOf(count: number, each: number, prefix = 'msg') {
  return Array.from({ length: count }, (_, t) => ({
    seq: t + 1,
    at: '2026-01-01T00:00:00Z',
    messages: Array.from({ length: each }, (_, m) => ({
      role: m === 0 ? 'user' : 'assistant',
      content: { parts: [{ type: 'text', text: `${prefix}-${t}-${m}` }] },
    })),
    artifacts: [],
  }));
}

test('mounts a screenful of a long session, not the whole render window', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1200, height: 800 });
  await page.addInitScript(mockBackend as never, {
    workspace: '/Users/me/projects/opencraft',
    listSessions: [
      {
        id: 's-long',
        title: 'Long session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 315,
        total_tokens: 0,
      },
    ],
    // Six turns of 45 messages arrive with hydration: 270 messages, more
    // than the 200-message render window, with the window's first turn
    // starting mid-turn (the flat shape the transcript falls back on).
    sessionTurns: turnsOf(7, 45),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Long session' }).click();

  const scroller = page.getByTestId('chat-scroll');
  const rows = scroller.locator('[data-msg-index]');
  await expect(page.getByText('msg-6-44', { exact: true })).toBeVisible();
  // The DOM is what the reader can reach, not what is loaded: a screenful
  // of rows around the newest one, out of the 200 messages the render
  // window holds. What the band contains is measured in items, and a turn
  // whose steps are folded is three of them however many messages it
  // carries — so the count is what says the DOM is small, and where it
  // sits is what says it is at the reader.
  expect(await rows.count()).toBeLessThan(40);
  await expect.poll(() => rowsOnScreen(scroller)).toBe(true);
  // The head of the render window — message 70 of the 270 hydrated ones,
  // which lands inside a turn — is not mounted, and neither is anything
  // above it: the list mounts a screenful around the reader, not the
  // window the store keeps loaded.
  await expect(page.getByText('msg-2-25', { exact: true })).not.toBeAttached();
  const oldestRendered = () =>
    rows.evaluateAll((els) =>
      Math.min(...els.map((el) => Number(el.dataset.msgIndex))),
    );
  const oldest = await oldestRendered();

  // Reading upwards mounts the rows above as they come into view — and
  // widens the render window to the whole loaded transcript — while the
  // DOM stays a screenful.
  await scroller.evaluate((el) => {
    el.scrollTo(0, 0);
  });
  await expect(page.getByText('msg-1-0', { exact: true })).toBeVisible();
  expect(await oldestRendered()).toBeLessThan(oldest);
  expect(await rows.count()).toBeLessThan(40);

  // The list reserves the height of the items it has not measured, so the
  // scrollbar describes the whole transcript and the end of it really is
  // the end: reading to the bottom lands the last row's own edge on the
  // list's.
  await scroller.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await expect(page.getByText('msg-6-44', { exact: true })).toBeVisible();
  const bottomGap = await scroller.evaluate((el) => {
    const list = el.querySelector('[data-transcript-list]');
    const last = list?.lastElementChild;
    if (!list || !last) return Number.POSITIVE_INFINITY;
    return Math.abs(
      list.getBoundingClientRect().bottom - last.getBoundingClientRect().bottom,
    );
  });
  expect(bottomGap).toBeLessThan(40);
});

test('renders a 5000-message session inside the render window', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-huge',
        title: 'Huge session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 5000,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(5000),
  });
  await page.goto('/');

  const started = Date.now();
  await page.getByRole('button', { name: 'Huge session' }).click();
  await expect(page.getByText('message-4999')).toBeVisible({
    timeout: 10_000,
  });
  // Generous absolute threshold: windowed rendering of a 5000-message
  // session must stay interactive on CI-class hardware.
  expect(Date.now() - started).toBeLessThan(10_000);
});

// patchOf synthesizes a unified diff with `lines` added lines, the shape
// a long turn's apply_patch calls carry: the body a tool card must keep
// folded, and the weight a turn's rows would carry if it did not.
function patchOf(lines: number): string {
  const body = Array.from(
    { length: lines },
    (_, i) => `+const value${i} = compute(${i});`,
  ).join('\n');
  return [
    'diff --git a/src/file.ts b/src/file.ts',
    'index 1111111..2222222 100644',
    '--- a/src/file.ts',
    '+++ b/src/file.ts',
    `@@ -1,3 +1,${lines} @@`,
    body,
  ].join('\n');
}

function heavyTurns(count: number, patchesPerTurn: number): unknown[] {
  return Array.from({ length: count }, (_, turn) => {
    const parts: unknown[] = [];
    const results: unknown[] = [];
    for (let i = 0; i < patchesPerTurn; i += 1) {
      const callID = `call-${turn}-${i}`;
      parts.push({
        type: 'tool_call',
        call: {
          id: callID,
          name: 'apply_patch',
          arguments: { patch: patchOf(200) },
        },
      });
      results.push({
        type: 'tool_result',
        result: {
          call_id: callID,
          content: { parts: [{ type: 'text', text: 'ok' }] },
          is_error: false,
        },
      });
    }
    return {
      seq: turn + 1,
      at: '2026-01-01T00:00:00Z',
      artifacts: [],
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: `prompt ${turn}` }] },
        },
        { role: 'assistant', content: { parts } },
        { role: 'tool', content: { parts: results } },
        {
          role: 'assistant',
          content: {
            parts: [{ type: 'text', text: `final answer ${turn}` }],
          },
        },
      ],
    };
  });
}

// TURNS is the session the walk below expands: twelve turns of eight
// 200-line patches each.
const TURNS = 12;

test('expanding a long turn stays bounded', async ({ page }) => {
  // The walk navigates with the ruler, which is drawn from the md
  // breakpoint up: pin the viewport so the walk's affordance is there.
  await page.setViewportSize({ width: 1200, height: 800 });
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-heavy',
        title: 'Heavy session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 120,
        total_tokens: 0,
      },
    ],
    sessionTurns: heavyTurns(TURNS, 8),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Heavy session' }).click();
  await expect(page.getByText('final answer 11')).toBeVisible();

  const nodes = () =>
    page.evaluate(() => document.getElementsByTagName('*').length);
  const before = await nodes();
  // Page the whole session in first: the archive hands the older turns
  // over a page at a time, and a turn the archive has not handed over has
  // no ruler tick to ask for.
  await loadAllHistory(page);
  // Open every turn's fold, newest first, walking back up the session.
  //
  // The walk asks the ruler for each turn instead of hunting the DOM for
  // folds: the transcript is windowed, so a fold exists only while the
  // reader is near it, and a walk that clicks "whatever is mounted" is a
  // walk whose progress depends on the list dragging the reader along as
  // it re-measures. That dependency is what made this test flaky — it was
  // not the app moving the reader but Playwright's own click scrolling a
  // target into view, and whenever the drag stopped early (the fold in
  // reach was already on screen, or a re-render replaced the row under the
  // click) the walk broke at the top with turns still folded, or sat on a
  // locator that never resolved until the test timed out. A jump is a
  // reader's own way to reach a turn, and the fold at its head is right
  // there once it lands.
  const started = Date.now();
  let opened = 0;
  for (let turn = TURNS - 1; turn >= 0; turn -= 1) {
    // The compact ruler draws a tick per turn; twelve turns is well under
    // the dense-ruler threshold, so every turn has one.
    await page.locator(`[data-peek-index="${turn}"]`).click();
    const fold = page
      .locator(`[data-turn-index="${turn}"]`)
      .getByRole('button', { name: /Worked for/ });
    // The click has to land on this turn's fold, and the fold has to be
    // the one that changes state: a jump that missed would otherwise look
    // like a turn that had nothing to open.
    await expect(fold).toHaveAttribute('aria-expanded', 'false');
    await fold.click();
    await expect(fold).toHaveAttribute('aria-expanded', 'true');
    opened += 1;
  }
  const elapsed = Date.now() - started;
  const after = await nodes();
  console.log(
    `[perf] expand turns=${opened} nodes ${before} -> ${after} in ${elapsed}ms`,
  );

  // All twelve turns were opened, and the walk is bounded by the number
  // of turns rather than by the patch lines behind them: one jump and one
  // click per turn, not a step for every row the folds uncovered.
  expect(opened).toBe(TURNS);
  expect(elapsed).toBeLessThan(15_000);
  expect(after).toBeLessThan(20_000);
  // Expanding twelve turns of eight 200-line patches must add hundreds of
  // nodes, not tens of thousands: the patch bodies stay folded, and the
  // rows that hold them are mounted only where the reader is looking.
  expect(after - before).toBeLessThan(5_000);
  // Patch bodies stay folded until a card is opened.
  const diffRows = await page.locator('div[class*="grid-cols-"]').count();
  expect(diffRows).toBeLessThan(200);
  expect(after).toBeGreaterThanOrEqual(before);
});

// processTurn builds one turn whose process list is `rows` assistant
// messages, the shape that used to mount every row when the "Worked for"
// header was expanded.
function processTurn(rows: number): unknown[] {
  const messages: unknown[] = [
    {
      role: 'user',
      content: { parts: [{ type: 'text', text: 'long running prompt' }] },
    },
  ];
  for (let i = 0; i < rows; i += 1) {
    messages.push({
      role: 'assistant',
      content: {
        parts: [
          // Each step carries visible text, so it becomes its own
          // transcript row instead of merging into the previous one.
          { type: 'text', text: `step ${i}` },
          {
            type: 'tool_call',
            call: {
              id: `call-${i}`,
              name: 'exec_command',
              arguments: { cmd: `step ${i}` },
            },
          },
        ],
      },
    });
  }
  messages.push({
    role: 'assistant',
    content: { parts: [{ type: 'text', text: 'all steps done' }] },
  });
  return [
    {
      seq: 1,
      at: '2026-01-01T00:00:00Z',
      artifacts: [],
      messages,
    },
  ];
}

test('windows a long turn instead of mounting every row', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-steps',
        title: 'Many steps',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 200,
        total_tokens: 0,
      },
    ],
    // A tall turn before the one that is expanded: without it the expanded
    // rows start at the scroller's origin, and a list that measured the
    // viewport against its own top instead of its position in the
    // transcript would look right by accident.
    sessionTurns: [...fillerTurn(60), ...processTurn(120)],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Many steps' }).click();
  await expect(page.getByText('all steps done')).toBeVisible();

  const scroller = page.getByTestId('chat-scroll');
  const rows = scroller.locator('[data-msg-index]');
  const mounted = () => rows.count();
  const reserved = () =>
    scroller.evaluate((el) => el.scrollHeight / el.clientHeight);
  const folded = await mounted();

  await page.getByRole('button', { name: /Worked for/ }).click();
  // The turn's 120 steps are rows of the windowed transcript now, and
  // opening the fold mounts the ones at the reader, not the turn: the DOM
  // holds a screenful, while the list reserves the height of the rest, so
  // the scrollbar still describes the whole turn.
  await expect.poll(mounted).toBeGreaterThan(folded);
  expect(await mounted()).toBeLessThan(60);
  expect(await reserved()).toBeGreaterThan(5);

  // The window is where the reader is — a slice mounted against the list's
  // own top rather than its position in the transcript would land the rows
  // somewhere else entirely and leave this stretch empty...
  await expect.poll(() => rowsOnScreen(scroller)).toBe(true);
  // ...at the far end of the turn, in the middle, and at its head.
  await scroller.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await expect(page.getByText('all steps done')).toBeVisible();
  await expect.poll(() => rowsOnScreen(scroller)).toBe(true);
  expect(await mounted()).toBeLessThan(60);

  await scroller.evaluate((el) => el.scrollTo(0, el.scrollHeight / 2));
  await expect.poll(() => rowsOnScreen(scroller)).toBe(true);
  expect(await mounted()).toBeLessThan(60);

  await scroller.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('filler paragraph 0')).toBeVisible();
  await expect.poll(() => rowsOnScreen(scroller)).toBe(true);
  expect(await mounted()).toBeLessThan(60);
});

// rowsOnScreen answers whether any mounted transcript row is inside the
// scroller's box: the transcript is windowed, so the rows on screen are
// the ones the list decided to mount.
function rowsOnScreen(scroller: Locator): Promise<boolean> {
  return scroller.evaluate((el) => {
    const box = el.getBoundingClientRect();
    return Array.from(el.querySelectorAll('[data-transcript-index]')).some(
      (row) => {
        const r = row.getBoundingClientRect();
        return r.bottom > box.top && r.top < box.bottom;
      },
    );
  });
}

// fillerTurn is a turn that is tall without being a process list: a reply
// of `paragraphs` blocks, enough to push whatever follows it down the
// scroller.
function fillerTurn(paragraphs: number): unknown[] {
  return [
    {
      seq: 1,
      at: '2026-01-01T00:00:00Z',
      artifacts: [],
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: 'read this first' }] },
        },
        {
          role: 'assistant',
          content: {
            parts: [
              {
                type: 'text',
                text: Array.from(
                  { length: paragraphs },
                  (_, i) => `filler paragraph ${i}`,
                ).join('\n\n'),
              },
            ],
          },
        },
      ],
    },
  ];
}
