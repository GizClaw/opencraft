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
