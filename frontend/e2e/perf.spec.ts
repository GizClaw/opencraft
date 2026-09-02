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

test('renders a 5000-message session inside the render window', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    listSessions: [
      {
        id: 's-huge',
        title: 'Huge session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 5000,
        total_tokens: 0,
      },
    ],
    sessionTurns: turns(5000),
  });
  await page.goto('/');

  const started = Date.now();
  await page.getByRole('button', { name: 'Huge session' }).click();
  await expect(
    page.getByRole('button', { name: /earlier messages/i }),
  ).toBeVisible({
    timeout: 10_000,
  });
  // Generous absolute threshold: windowed rendering of a 5000-message
  // session must stay interactive on CI-class hardware.
  expect(Date.now() - started).toBeLessThan(10_000);
  await expect(page.getByText('message-4999')).toBeVisible();
});
