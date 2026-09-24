// The composer floats over the transcript instead of taking a row under
// it: the message list keeps the whole column, rows scroll behind the
// card, and the list reserves the card's height as bottom padding so the
// newest reply always clears the card.
import { expect, test, type Locator, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { loadAllHistory, typeComposerMessage } from './helpers';

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

async function openSession(page: Page) {
  await page.getByRole('button', { name: 'Move the store module' }).click();
}

function bottom(locator: Locator) {
  return locator.evaluate((el) => el.getBoundingClientRect().bottom);
}

test('floats over the transcript and keeps the newest reply clear', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [
      {
        id: 's-1',
        title: 'Move the store module',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 120,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(60),
  });
  await page.goto('/');
  await openSession(page);
  await expect(page.getByText('reply-59')).toBeVisible();

  const card = page.getByTestId('composer');
  const scroll = page.getByTestId('chat-scroll');
  const cardBox = await card.boundingBox();
  const scrollBox = await scroll.boundingBox();
  expect(cardBox).not.toBeNull();
  expect(scrollBox).not.toBeNull();
  if (!cardBox || !scrollBox) return;

  // The transcript runs the full column, so rows pass behind the card.
  expect(scrollBox.y + scrollBox.height).toBeGreaterThanOrEqual(
    cardBox.y + cardBox.height - 2,
  );
  // Pinned to the bottom, the newest row stops above the card.
  expect(await bottom(page.getByText('reply-59'))).toBeLessThan(cardBox.y);

  // Nothing fades and nothing is painted around the card: the wrapper is
  // exactly as tall as the card plus its bottom gap (no band above it),
  // and neither the wrapper nor anything inside it lays down a gradient
  // scrim, so a row's contrast survives right up to the card's edge.
  const overlay = card.locator('..');
  const overlayBox = await overlay.boundingBox();
  expect(overlayBox?.y).toBeCloseTo(cardBox.y, 0);
  expect(overlayBox?.height).toBeCloseTo(cardBox.height + 16, 0);
  const paint = await overlay.evaluate((el) => ({
    image: getComputedStyle(el).backgroundImage,
    gradient: [...el.querySelectorAll('*')]
      .map((node) => getComputedStyle(node).backgroundImage)
      .filter((image) => image.includes('gradient')),
  }));
  expect(paint.image).toBe('none');
  expect(paint.gradient).toEqual([]);
  // A fade faked with a mask on the scroller would count too.
  expect(await scroll.evaluate((el) => getComputedStyle(el).maskImage)).toBe(
    'none',
  );

  // Scrolling down the middle of the transcript still leaves the newest
  // row — and the card — where they were: the card floats, it is not a
  // row that the scroll position can move.
  // Hydration starts from the newest turns only, so page the rest in
  // first, then read the session from its oldest end: what the card has
  // to survive is the transcript moving under it, and the rows that are
  // in the DOM are the ones the reader is looking at (the transcript is
  // windowed, so a row far below the viewport is not mounted at all).
  await loadAllHistory(page);
  await scroll.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('ask-0', { exact: true })).toBeVisible();
  const parked = await card.boundingBox();
  expect(parked?.y).toBeCloseTo(cardBox.y, 0);
});

test('reserves more room as the card grows', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Summarize the refactor');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.evaluate(
    (data) =>
      (
        window as never as {
          __emit: (name: string, data: unknown) => void;
        }
      ).__emit('opencraft:ui', data),
    {
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'Here is the summary.\n' },
        },
      },
    },
  );
  await expect(page.getByText('Here is the summary.')).toBeVisible();

  const reserved = () =>
    page
      .getByTestId('chat-scroll')
      .evaluate((el) => parseFloat(getComputedStyle(el).paddingBottom));
  const roomy = await reserved();
  const card = await page.getByTestId('composer').boundingBox();

  // A draft that wraps: the card grows, so the reserved room must grow
  // with it or the last reply ends up under the card.
  await typeComposerMessage(
    page,
    'A draft long enough to wrap onto several lines in the composer, so the card is taller than before.',
  );
  await expect(async () => {
    expect(await reserved()).toBeGreaterThan(roomy);
  }).toPass();
  await expect(async () => {
    const grown = await page.getByTestId('composer').boundingBox();
    expect(grown?.y).toBeLessThan((card?.y ?? 0) - 20);
  }).toPass();
});

test('the margins around the card still scroll the transcript', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [
      {
        id: 's-1',
        title: 'Move the store module',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 120,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(60),
  });
  await page.goto('/');
  await openSession(page);
  await expect(page.getByText('reply-59')).toBeVisible();

  const scroll = page.getByTestId('chat-scroll');
  const card = await page.getByTestId('composer').boundingBox();
  if (!card) return;
  // The wrapper paints nothing but stays click-through: the gutter beside
  // the card belongs to the transcript, so the wheel still reaches the
  // scroller behind it.
  const before = await scroll.evaluate((el) => el.scrollTop);
  await page.mouse.move(card.x - 10, card.y + card.height / 2);
  await page.mouse.wheel(0, -400);
  await expect(async () => {
    expect(await scroll.evaluate((el) => el.scrollTop)).toBeLessThan(before);
  }).toPass();
});

// Regression: a markdown prefix must not trap the caret. Typing "- "
// turns the line into a list, and since Enter is the send key a soft
// break was the only way to a second line — inside the bullet, with no
// route back to the left margin.
test('lets a markdown list go without leaving the composer', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  const editor = page.locator('.ProseMirror').first();

  await typeComposerMessage(page, '- one');
  await expect(editor.locator('li')).toHaveText('one');
  // The first break continues the bullet …
  await editor.press('Shift+Enter');
  // … and the next one steps out of the list, so the draft carries on at
  // the left margin instead of another indented line.
  await editor.press('Shift+Enter');
  await editor.pressSequentially('plain');
  await expect(editor.locator('li')).toHaveText('one');
  await expect(editor.locator('p', { hasText: 'plain' })).toHaveText('plain');
  expect(await editor.locator('li p', { hasText: 'plain' }).count()).toBe(0);

  // What is sent keeps both: the bullet above and the plain line below.
  await page.getByRole('button', { name: 'Send' }).click();
  const transcript = page.getByTestId('chat-scroll');
  await expect(transcript.locator('li')).toContainText('one');
  await expect(transcript.locator('li p', { hasText: 'plain' })).toHaveCount(0);
  await expect(transcript.getByText('plain')).toBeVisible();

  // … and the same escape backwards: Backspace at the start of a bullet
  // drops the prefix and keeps the text.
  await typeComposerMessage(page, '- two');
  await expect(editor.locator('li')).toHaveText('two');
  await editor.press('Home');
  await editor.press('Backspace');
  await expect(editor.locator('li')).toHaveCount(0);
  await expect(editor.locator('p').first()).toHaveText('two');
});
