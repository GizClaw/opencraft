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

// The panel clips its own overflow, so anything it opens has to be laid
// out against the window. That only holds while the panel is not a
// containing block for `position: fixed`: an entrance animation filled
// forwards leaves an identity matrix behind, and the tool dialogs then
// lose their header and their save bar to the panel's clip.
test('lays fixed children out against the window, not the panel', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.addInitScript(mockBackend as never, {});
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const panel = page.locator(
    '[role="dialog"][aria-labelledby="settings-title"]',
  );
  await expect(panel).toBeVisible();
  // The entrance itself is a transform; the panel is expected to be a
  // containing block only while it plays.
  await panel.evaluate(async (el) => {
    await Promise.all(el.getAnimations().map((a) => a.finished));
  });
  const probe = await panel.evaluate((el) => {
    const node = document.createElement('div');
    node.style.cssText = 'position:fixed;inset:0;pointer-events:none';
    el.appendChild(node);
    const rect = node.getBoundingClientRect();
    node.remove();
    return { width: Math.round(rect.width), height: Math.round(rect.height) };
  });
  expect(probe).toEqual({ width: 1280, height: 900 });
});
