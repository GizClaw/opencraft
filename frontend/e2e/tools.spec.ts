import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

// Settings > Tools: the provider-specific knobs of the generation tools.
// The model-facing tools keep the common parameters, so this tab is the
// only place a driver's own vocabulary (OpenAI editing knobs, Seedream
// size tokens, …) is configured.
const STATE = {
  image: {
    instances: [
      {
        id: 'openai-inst-1',
        label: 'OpenAI',
        impl: 'openai',
        managed: false,
        fields: [
          {
            name: 'background',
            kind: 'enum',
            values: ['auto', 'opaque', 'transparent'],
          },
          { name: 'output_compression', kind: 'int', min: 0, max: 100 },
        ],
        values: { background: 'transparent' },
      },
    ],
  },
  video: { instances: [] },
};

test('edits and saves provider knobs from the tools tab', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Config.ToolOptions': `async () => (${JSON.stringify(STATE)})`,
        'Config.SaveToolOptions': `async (req) => { globalThis.__savedToolOptions = req; }`,
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Tools' }).click();

  await expect(
    page.getByText('Image generation', { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel('Background')).toContainText('transparent');
  // A tool with no eligible deployment says so and cannot be saved.
  await expect(
    page.getByText('No enabled deployment serves this output yet.'),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Save & apply' }).nth(1),
  ).toBeDisabled();

  await page.getByLabel('Background').click();
  await page.getByRole('option', { name: 'opaque' }).click();
  await page.getByLabel('Compression').fill('70');
  await page.getByRole('button', { name: 'Save & apply' }).first().click();

  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            globalThis as unknown as {
              __savedToolOptions?: {
                image?: Record<string, Record<string, unknown>>;
              };
            }
          ).__savedToolOptions?.image?.['openai-inst-1']?.background,
      ),
    )
    .toBe('opaque');
  await expect(page.getByText('Saved')).toBeVisible();
});
