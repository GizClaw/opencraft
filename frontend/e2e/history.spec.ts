import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

function turns(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    seq: i + 1,
    at: '2026-01-01T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: `message-${i}` }] },
      },
    ],
    artifacts: [],
  }));
}

test('resumes a long session with windowed transcript', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-old',
        title: 'Long session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 250,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(250),
  });
  await page.goto('/');

  // The sidebar lists the resumed session; click it to load history.
  await page.getByRole('button', { name: 'Long session' }).click();
  await expect(page.getByText('message-249')).toBeVisible();
  await expect(page.getByText('message-0')).not.toBeVisible();

  // Reaching the top auto-loads the next history window; the loaded
  // rows appear above the current position, so scroll up again to read
  // them once the window has been expanded.
  const scroller = page.getByTestId('chat-scroll');
  await scroller.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('message-50')).toBeVisible();
  await scroller.evaluate((el) => el.scrollTo(0, 0));
  await expect(page.getByText('message-0')).toBeVisible();
});
