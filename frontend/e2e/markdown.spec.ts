import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

// A message whose prose carries long paths and a long URL. Neither the
// transcript nor the user bubble scrolls sideways, so anything that
// cannot wrap is clipped at the column edge — the bug this pins.
const LONG_TOKENS = [
  '看一下 ./internal/capabilities/tools/applypatch/tool.go 和',
  'https://example.com/a/very/long/path/that/keeps/going/and/going?x=1 这段',
].join(' ');

const ANSWER = [
  '## 对 OpenCraft 的影响（已实测）',
  '',
  '- 我们 pin 的是 `core v0.4.5`（`go.mod:6`），用临时 modfile（`/tmp/oc-bump.mod`，仓库没动）在 `v0.4.6` 下跑：`go build ./...` 通过；`./internal/orchestration/{engine,host,interact}`、`./internal/capabilities/sessions{,/state}`、`./internal/capabilities/tools/applypatch` 测试全绿。',
].join('\n');

function turns() {
  return [
    {
      seq: 1,
      at: '2026-01-01T00:00:00Z',
      artifacts: [],
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: LONG_TOKENS }] },
        },
        {
          role: 'assistant',
          content: { parts: [{ type: 'text', text: ANSWER }] },
        },
      ],
    },
  ];
}

test('long inline code and URLs wrap inside the transcript column', async ({
  page,
}) => {
  await page.setViewportSize({ width: 980, height: 720 });
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-md',
        title: 'Markdown',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 2,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Markdown' }).click();
  await expect(page.getByText('对 OpenCraft 的影响')).toBeVisible();

  const scroller = page.getByTestId('chat-scroll');
  // Nothing in the transcript may exceed the column: a long path in prose
  // or inside a user bubble has to wrap, not push a scrollbar.
  await expect
    .poll(() => scroller.evaluate((el) => el.scrollWidth - el.clientWidth))
    .toBeLessThanOrEqual(1);

  // The answer keeps its structure: the bullet's inline code is still one
  // paragraph line box, not a stack of one-token-per-line fragments.
  await expect(page.getByText('core v0.4.5')).toBeVisible();
  await expect(page.getByText('测试全绿', { exact: false })).toBeVisible();
});

// The user bubble's half of the same promise. The pill is a shrink-wrapped
// flex item, so its width is fit-content — and fit-content's floor is the
// longest unbreakable run in the text, which `break-word` does not lower.
// One pasted token longer than the 80% cap (a hash, a key, a queryless
// path) used to pin the pill to its own width and, because the row is
// right-aligned, slide the beginning of the message under the viewport:
// invisible, and no scrollbar reaches it.
test('a single unbreakable run stays inside the user bubble', async ({
  page,
}) => {
  await page.setViewportSize({ width: 980, height: 720 });
  const run = `sha256-${'aB3x9Z'.repeat(70)}`;
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-run',
        title: 'Unbreakable',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 1,
        total_tokens: 0,
      },
    ],
    sessionTurns: [
      {
        seq: 1,
        at: '2026-01-01T00:00:00Z',
        artifacts: [],
        messages: [
          { role: 'user', content: { parts: [{ type: 'text', text: run }] } },
        ],
      },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Unbreakable' }).click();

  const bubble = page.locator('.user-bubble-md');
  await expect(bubble).toBeVisible();
  const scroller = page.getByTestId('chat-scroll');
  const [bubbleBox, scrollBox] = await Promise.all([
    bubble.boundingBox(),
    scroller.boundingBox(),
  ]);
  // Both edges of the pill sit inside the column, and the run wrapped
  // instead of being squeezed onto one clipped line.
  expect(bubbleBox!.x).toBeGreaterThanOrEqual(scrollBox!.x - 1);
  expect(bubbleBox!.x + bubbleBox!.width).toBeLessThanOrEqual(
    scrollBox!.x + scrollBox!.width + 1,
  );
  expect(bubbleBox!.height).toBeGreaterThan(40);
  // And the wrapped pill adds no sideways scroll of its own.
  await expect
    .poll(() => scroller.evaluate((el) => el.scrollWidth - el.clientWidth))
    .toBeLessThanOrEqual(1);
});
