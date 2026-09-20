// The turn ruler hangs in the transcript's left gutter. Past
// DENSE_TICK_THRESHOLD it draws the turns around the viewport (the
// slice) at the compact pitch: one dash per turn, the block hugging its
// content in the middle of the column, the cut end fading. It has to
// stay on the same axis as the compact ruler (inside the column, clear
// of the sidebar and of the message text) and still jump to the turn
// under the pointer.
import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { loadAllHistory } from './helpers';

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
        messages: 280,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(140),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Move the store module' }).click();
  await expect(
    page.getByText('reply-139', { exact: true }).first(),
  ).toBeVisible();
  // The ruler needs the session's turns, and hydration only pulls the
  // newest page from the archive.
  await loadAllHistory(page);
  // Paging leaves the viewport at the oldest loaded rows; the ruler is
  // asserted at the newest end, so return there first.
  await page
    .getByTestId('chat-scroll')
    .evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await page.waitForTimeout(120);

  const scroll = page.getByTestId('chat-scroll');
  const scrollBox = await scroll.boundingBox();
  expect(scrollBox).not.toBeNull();
  if (!scrollBox) return;

  // The ruler stopped at the newest turn, so the slice is clamped
  // there: the block is shorter than a full 41-dash window, its last
  // dash is the newest turn, and only the top was cut.
  const dashes = page.locator('[data-peek-tick]');
  await expect(dashes.first()).toBeVisible();
  const count = await dashes.count();
  expect(count).toBeGreaterThanOrEqual(21);
  expect(count).toBeLessThanOrEqual(41);
  expect(await dashes.last().getAttribute('data-peek-tick')).toBe('139');
  expect(await dashes.last().evaluate((el) => el.className)).toContain(
    'bg-accent',
  );

  // One dash per turn at the compact pitch, and the dashes stay in the
  // gutter instead of running under the message text.
  const boxes = await dashes.evaluateAll((els) =>
    els.map((el) => {
      const rect = el.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    }),
  );
  const pitch = boxes[1].y - boxes[0].y;
  expect(pitch).toBeGreaterThan(11);
  expect(pitch).toBeLessThan(12.5);
  for (const box of boxes) {
    expect(box.x).toBeGreaterThanOrEqual(scrollBox.x);
    expect(box.x + box.width).toBeLessThanOrEqual(scrollBox.x + 32);
  }

  // The block hugs its content and sits in the middle of the column.
  const peekBox = await page.getByTestId('message-peek').boundingBox();
  expect(peekBox).not.toBeNull();
  if (!peekBox) return;
  const last = boxes[boxes.length - 1];
  const blockCenter = (boxes[0].y + last.y + last.height) / 2;
  expect(Math.abs(blockCenter - (peekBox.y + peekBox.height / 2))).toBeLessThan(
    3,
  );

  // Only the cut end fades; the transcript's own end is a hard edge.
  const band = page.locator('[data-peek-cut]');
  expect(await band.getAttribute('data-peek-cut')).toBe('top');
  const mask = await band.evaluate((el) => {
    const style = getComputedStyle(el);
    return `${style.getPropertyValue('mask-image')} ${style.getPropertyValue(
      '-webkit-mask-image',
    )}`;
  });
  expect(mask).toContain('linear-gradient');

  // The scrubber's hit area covers that block and stays inside the
  // column as well: a wider target is fine, spilling over the sidebar
  // is not.
  const scrubber = page.getByRole('slider');
  const scrubberBox = await scrubber.boundingBox();
  expect(scrubberBox).not.toBeNull();
  if (!scrubberBox) return;
  expect(scrubberBox.x).toBeGreaterThanOrEqual(scrollBox.x);
  expect(scrubberBox.x + scrubberBox.width).toBeLessThanOrEqual(
    scrollBox.x + 48,
  );
  expect(Math.abs(scrubberBox.height - (count * 11.76 - 3.92))).toBeLessThan(3);

  // A quarter of the way down the block is a quarter of the way through
  // the turns it draws, and the jump lands that turn's first row at the
  // top of the transcript.
  const first = Number(await dashes.first().getAttribute('data-peek-tick'));
  const target = first + Math.floor(count / 4);
  await page.mouse.click(
    scrubberBox.x + scrubberBox.width / 2,
    scrubberBox.y + scrubberBox.height * 0.25,
  );
  await expect(
    page.getByText(`ask-${target}`, { exact: true }).first(),
  ).toBeInViewport();
  await expect(
    page.getByText('reply-139', { exact: true }).first(),
  ).not.toBeInViewport();
});
