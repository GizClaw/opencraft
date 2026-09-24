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

// loadAllHistory pages a resumed session back to its first turn by scrolling
// to the top until the transcript stops growing. Hydration only pulls the
// newest turns from the archive, so specs that assert on a whole long session
// call this first and then read it as one transcript.
export async function loadAllHistory(page: Page, maxScrolls = 40) {
  const scroller = page.getByTestId('chat-scroll');
  const height = () => scroller.evaluate((el) => el.scrollHeight);
  // Nothing in the DOM says "this is the session's first turn": row and turn
  // indices count from the oldest *loaded* message, so they restart at 0 on
  // every page, and the transcript is windowed, so the mounted rows are a
  // screenful whatever is loaded. What does say it is the scroll range: each
  // page lands above the reader and makes the transcript taller, and the
  // scripted paging stops once the archive is exhausted.
  let previous = await height();
  let stable = 0;
  for (let i = 0; i < maxScrolls && stable < STABLE_ROUNDS; i += 1) {
    await scroller.evaluate((el) => el.scrollTo(0, 0));
    await page.waitForTimeout(80);
    const next = await height();
    stable = next > previous + 1 ? 0 : stable + 1;
    previous = next;
  }
}

// Rounds without a taller transcript before paging is taken to be done. A
// page is a round trip against the in-page backend, so three rounds of quiet
// (~240ms) is already generous; five keeps a loaded CI runner from calling it
// early.
const STABLE_ROUNDS = 5;
