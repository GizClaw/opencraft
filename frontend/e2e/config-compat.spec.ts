// End-to-end coverage for the diagnostics compatibility repair. A user
// layer upgraded from before the resolver-based assembly can still name
// the retired ${env:OPEN_CRAFT_*} variables, which blocks runtime
// assembly; the button next to "Reload runtime" clears those
// declarations and reports what it removed.
import { expect, test } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

test('repairs obsolete config references from Settings > Diagnostics', async ({
  page,
}) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: handlerSources({
        'Diagnostics.RepairConfigCompat': async () => ({
          file: '/home/user/.opencraft/config/opencraft.yaml',
          backup: '/home/user/.opencraft/config/opencraft.yaml.bak',
          removed: ['agents.assistant.prepare[0]'],
        }),
      }),
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Diagnostics' }).click();

  await page
    .getByRole('button', { name: 'Repair config compatibility' })
    .click();
  await expect(
    page.getByText(
      'Removed 1 obsolete config declaration(s): ' +
        'agents.assistant.prepare[0]. Reload the runtime to apply.',
    ),
  ).toBeVisible();
});

test('reports when the layer has nothing to repair', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Diagnostics' }).click();

  await page
    .getByRole('button', { name: 'Repair config compatibility' })
    .click();
  await expect(
    page.getByText('No obsolete config references found.'),
  ).toBeVisible();
});
