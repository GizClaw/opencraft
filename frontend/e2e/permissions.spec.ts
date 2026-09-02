import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

test('renders an approval prompt and submits the choice', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await page
    .getByPlaceholder('What shall we craft today?')
    .fill('run a command');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText('run a command')).toBeVisible();
  // The approval prompt arrives mid-turn, after a user message exists.
  await page.waitForTimeout(300);

  await page.evaluate(() => {
    (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
      'opencraft:ui',
      {
        type: 'interact',
        data: {
          id: 'p-1',
          run_id: 'r-1',
          conversation_id: 's-1',
          kind: 'select',
          title: 'Allow running rm -rf?',
          body: [{ type: 'text', text: 'Command is not allowed' }],
          options: [
            { label: 'Allow once', value: 'allow_once' },
            { label: 'Deny', value: 'deny' },
          ],
          multi: false,
          allow_other: false,
          source: 'test',
        },
      },
    );
  });

  await expect(page.getByText('Allow running rm -rf?')).toBeVisible();
  await page.getByLabel('Allow once').check();
  await page.getByRole('button', { name: 'Submit' }).click();

  // Resolution removes the card.
  await page.evaluate(() => {
    (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
      'opencraft:ui',
      { type: 'resolved', data: { id: 'p-1' } },
    );
  });
  await expect(page.getByText('Allow running rm -rf?')).not.toBeVisible();
});
