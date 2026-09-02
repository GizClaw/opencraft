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

  await page.getByRole('button', { name: /earlier messages/i }).click();
  await expect(page.getByText('message-0')).toBeVisible();
});
