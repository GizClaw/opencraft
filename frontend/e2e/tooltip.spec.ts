// The shared hint layer (`data-tip`) draws one floating card. The card is
// capped at 22rem and clamps itself into the window, but the text inside
// it is not always prose: a host error string can carry a token with no
// break opportunity at all (a bearer token, a digest, a base64 blob), and
// a line that cannot break lays out as one line and paints past the
// card's border — off the window when the anchor sits near the edge. The
// card wraps that text like every other text surface in the app.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';

const WS = '/Users/me/projects/opencraft';
// 160 hex characters: no space, slash, dash or dot to break at.
const TOKEN = Array.from(
  { length: 160 },
  (_, i) => '0123456789abcdef'[i % 16],
).join('');
const HANDSHAKE_ERROR = `handshake failed: POST https://api.example.com/mcp returned 401 (token ${TOKEN})`;

// The card is `overflow: visible`, so a line that does not break is
// painted past the border without ever showing up in the element's own
// scroll metrics; the text is measured with a Range instead. One line box
// comes from the card's own line height, because "the token broke" has to
// be read off the rendered boxes, not off a class name.
async function measureHint(page: Page) {
  return page.getByTestId('tooltip').evaluate((el) => {
    const range = document.createRange();
    range.selectNodeContents(el);
    const text = range.getBoundingClientRect();
    const card = el.getBoundingClientRect();
    return {
      card: {
        left: card.left,
        right: card.right,
        width: card.width,
        height: card.height,
      },
      text: {
        left: text.left,
        right: text.right,
        width: text.width,
        height: text.height,
      },
      lineHeight: Number.parseFloat(getComputedStyle(el).lineHeight),
      windowWidth: window.innerWidth,
    };
  });
}

test('wraps a hint whose text has no break opportunity', async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 780 });
  await page.addInitScript(
    mockBackend as never,
    {
      workspace: WS,
      handlers: {
        'Config.MCPConfig':
          "async () => ([{ name: 'github', transport: 'stdio', command: 'npx' }])",
        // A server whose handshake failed: the status pill's hint is the
        // host's error string, token and all.
        'Config.MCPStatus': `async () => ([{ name: 'github', status: 'error', error: ${JSON.stringify(
          HANDSHAKE_ERROR,
        )} }])`,
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Tools' }).click();

  const pill = page.getByText('Connection failed', { exact: true });
  await expect(pill).toBeVisible();
  await pill.hover();
  const card = page.getByTestId('tooltip');
  await expect(card).toBeVisible();

  const hint = await measureHint(page);
  // The text laid out inside the card: a token that cannot break would
  // measure wider than the card it is painted in.
  expect(hint.text.width).toBeLessThanOrEqual(hint.card.width);
  // And it stays between the borders, on both sides.
  expect(hint.text.left).toBeGreaterThanOrEqual(hint.card.left);
  expect(hint.text.right).toBeLessThanOrEqual(hint.card.right);
  // The message is there in full, wrapped over several lines: a hint cut
  // down to one line (an ellipsis) would measure a single line box.
  expect(hint.text.height).toBeGreaterThan(2 * hint.lineHeight);
  // The card itself keeps the layer's promise of staying in the window.
  expect(hint.card.left).toBeGreaterThanOrEqual(0);
  expect(hint.card.right).toBeLessThanOrEqual(hint.windowWidth);
});
