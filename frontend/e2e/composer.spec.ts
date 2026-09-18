// The composer floats over the transcript instead of taking a row under
// it: the message list keeps the whole column, rows scroll behind the
// card, and the list reserves the card's height as bottom padding so the
// newest reply always clears the card.
import { expect, test, type Locator, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

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
  await scroll.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('ask-20')).toBeVisible();
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
