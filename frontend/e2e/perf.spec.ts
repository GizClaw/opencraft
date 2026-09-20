import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

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
// a long turn's apply_patch calls carry. The transcript used to mount
// one five-element row per patch line as soon as a turn's process rows
// were expanded.
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

test('expanding a long turn stays bounded', async ({ page }) => {
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
    sessionTurns: heavyTurns(12, 8),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Heavy session' }).click();
  await expect(page.getByText('final answer 11')).toBeVisible();

  const nodes = () =>
    page.evaluate(() => document.getElementsByTagName('*').length);
  const before = await nodes();
  const headers = page.getByRole('button', { name: /Worked for/ });
  const started = Date.now();
  const total = await headers.count();
  for (let i = 0; i < total; i += 1) {
    await headers.nth(i).click();
  }
  const elapsed = Date.now() - started;
  const after = await nodes();
  console.log(
    `[perf] expand turns=${total} nodes ${before} -> ${after} in ${elapsed}ms`,
  );

  // Budgets are generous for CI-class hardware; the point is that
  // expanding the whole session stays proportional to the number of
  // turns, not to the number of patch lines behind them.
  expect(elapsed).toBeLessThan(5_000);
  expect(after).toBeLessThan(20_000);
  // Expanding six turns of eight 200-line patches must add hundreds of
  // rows, not tens of thousands: the patch bodies stay folded and the
  // process list is windowed.
  expect(after - before).toBeLessThan(5_000);
  // Patch bodies stay folded until a card is opened.
  const diffRows = await page.locator('div[class*="grid-cols-"]').count();
  expect(diffRows).toBeLessThan(200);
  expect(after).toBeGreaterThanOrEqual(before);
});

// processTurn builds one turn whose process list is `rows` assistant
// messages, the shape that used to mount every row when the "耗时 /
// Worked for" header was expanded.
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

test('windows a long process list instead of mounting every row', async ({
  page,
}) => {
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
    sessionTurns: processTurn(120),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Many steps' }).click();
  await expect(page.getByText('all steps done')).toBeVisible();

  await page.getByRole('button', { name: /Worked for/ }).click();
  const rows = page.getByTestId('chat-scroll').locator('[data-index]');
  await expect.poll(() => rows.count()).toBeGreaterThan(0);
  // A screenful (plus overscan) of the 120 rows is mounted, not all of
  // them; the transcript still ends with the final answer.
  expect(await rows.count()).toBeLessThan(60);
  await expect(page.getByText('all steps done')).toBeVisible();
});
