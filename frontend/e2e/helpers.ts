import type { Page } from '@playwright/test';

// The chat composer is a TipTap contenteditable, not a <textarea>: it has
// no native placeholder attribute and text must be typed through the
// editor so ProseMirror receives the input.
export async function typeComposerMessage(page: Page, text: string) {
  const editor = page.locator('.ProseMirror').first();
  await editor.waitFor();
  await editor.click();
  await editor.pressSequentially(text);
}

// loadAllHistory pages a resumed session back to its first turn by
// scrolling to the top until the rendered row count stops growing.
// Hydration only pulls the newest turns from the archive, so specs that
// assert on a whole long session call this first and then read it as one
// transcript.
export async function loadAllHistory(page: Page, maxScrolls = 40) {
  const scroller = page.getByTestId('chat-scroll');
  const rendered = () => page.locator('[data-msg-index]').count();
  let previous = await rendered();
  let stable = 0;
  for (let i = 0; i < maxScrolls && stable < 3; i += 1) {
    await scroller.evaluate((el) => el.scrollTo(0, 0));
    await page.waitForTimeout(80);
    const next = await rendered();
    stable = next === previous ? stable + 1 : 0;
    previous = next;
  }
}
