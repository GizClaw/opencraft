// Settings is a window-level dialog, and its nav search resolves straight to
// the card it names. Both were reworked together (shared overlay stack plus
// the search index), so keep the framing and the jump pinned: a settings
// surface that silently turns into a full-bleed page, or a search that lands
// on the tab instead of the card, is exactly what this guards.
import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

test('opens as a centered dialog, inset from the window', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.addInitScript(mockBackend as never, {});
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const panel = page.locator(
    '[role="dialog"][aria-labelledby="settings-title"]',
  );
  await expect(panel).toBeVisible();
  const box = await panel.boundingBox();
  expect(box).not.toBeNull();
  // A dialog leaves the scrim visible around it instead of filling the
  // window, so every edge is inset.
  expect(box!.x).toBeGreaterThan(0);
  expect(box!.y).toBeGreaterThan(0);
  expect(box!.x + box!.width).toBeLessThan(1280);
  expect(box!.y + box!.height).toBeLessThan(900);
});

test('search jumps to the card it names', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {});
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('textbox', { name: 'Search settings' }).fill('tavily');
  await page.getByRole('button', { name: 'Web search' }).click();
  await expect(
    page.getByRole('tab', { name: 'Tools', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#settings-tools-websearch')).toBeInViewport();
});
