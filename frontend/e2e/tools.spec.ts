import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

// Settings > Tools: the generation tools (one item each, opened in a
// dialog) sit above the MCP server list. The model-facing tools keep the
// common parameters, so this tab is the only place a driver's own
// vocabulary (OpenAI editing knobs, Seedream size tokens, …) is set.
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

test('edits a generation tool from the tools tab', async ({ page }) => {
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

  // The generation tools are list items on the same tab as the MCP list.
  await expect(
    page.getByText('Image generation', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Video generation', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('No enabled deployment serves this output yet.'),
  ).toBeVisible();
  await expect(page.getByText('No MCP servers configured yet.')).toBeVisible();

  await page.getByText('Image generation', { exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Image generation' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('Background')).toContainText('transparent');

  await dialog.getByLabel('Background').click();
  await dialog.getByRole('option', { name: 'opaque' }).click();
  await dialog.getByLabel('Compression').fill('70');
  await dialog.getByRole('button', { name: 'Save & apply' }).click();

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
  await expect(dialog.getByText('Saved')).toBeVisible();
});
