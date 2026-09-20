// The turn ruler hangs in the transcript's left gutter. Past
// DENSE_TICK_THRESHOLD it becomes a full-height scrubber; it has to stay
// on the same axis as the compact ruler (inside the column, clear of the
// sidebar and of the message text) and still jump to the turn under the
// pointer.
import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

const WS = '/Users/me/projects/opencraft';

function turns(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    seq: i + 1,
    at: '2026-01-01T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: `ask-${i}` }] },
      },
      {
        role: 'assistant',
        content: { parts: [{ type: 'text', text: `reply-${i}` }] },
      },
    ],
    artifacts: [],
  }));
}

test('keeps the dense turn ruler inside the transcript gutter', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1100, height: 780 });
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [
      {
        id: 's-1',
        title: 'Move the store module',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 84,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(42),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Move the store module' }).click();
  await expect(
    page.getByText('reply-41', { exact: true }).first(),
  ).toBeVisible();

  const scroll = page.getByTestId('chat-scroll');
  const scrollBox = await scroll.boundingBox();
  expect(scrollBox).not.toBeNull();
  if (!scrollBox) return;

  // Every turn gets its own dash while 42 of them still fit.
  const dashes = page.locator('[data-peek-tick]');
  await expect(dashes).toHaveCount(42);
  for (let i = 0; i < 42; i += 1) {
    const box = await dashes.nth(i).boundingBox();
    if (!box) continue;
    expect(box.x).toBeGreaterThanOrEqual(scrollBox.x);
    expect(box.x + box.width).toBeLessThanOrEqual(scrollBox.x + 32);
  }

  // The scrubber's hit area stays inside the column as well: a wider
  // target is fine, spilling over the sidebar is not.
  const scrubberBox = await page.getByRole('slider').boundingBox();
  expect(scrubberBox).not.toBeNull();
  if (!scrubberBox) return;
  expect(scrubberBox.x).toBeGreaterThanOrEqual(scrollBox.x);
  expect(scrubberBox.x + scrubberBox.width).toBeLessThanOrEqual(
    scrollBox.x + 48,
  );

  // A quarter of the way down the scrubber is turn 11 (0-based 10), and
  // the jump lands its first row at the top of the transcript.
  await page.mouse.click(
    scrubberBox.x + scrubberBox.width / 2,
    scrubberBox.y + scrubberBox.height * 0.25,
  );
  await expect(
    page.getByText('ask-10', { exact: true }).first(),
  ).toBeInViewport();
  await expect(
    page.getByText('reply-41', { exact: true }).first(),
  ).not.toBeInViewport();
});
