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
            default: 'auto',
          },
          { name: 'output_compression', kind: 'int', min: 0, max: 100 },
          {
            name: 'input_fidelity',
            kind: 'enum',
            values: ['low', 'high'],
            default: 'low',
          },
        ],
        values: { background: 'transparent' },
        presets: [{ id: 'edit_fidelity', fields: { input_fidelity: 'high' } }],
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
  // The row names the default the driver documents.
  await expect(dialog.getByText('· default auto')).toBeVisible();

  await dialog.getByLabel('Background').click();
  // Leaving a knob alone is the documented path back to the provider
  // default, and the row names the default the driver documents.
  // The listbox is a floating layer portaled out of the dialog, so it is
  // queried at page level rather than under the dialog.
  await expect(
    page.getByRole('option', { name: 'Follow the provider default' }),
  ).toBeVisible();
  await page.getByRole('option', { name: 'opaque' }).click();
  await dialog.getByLabel('Compression').fill('70');
  // A preset stages its knobs in the form; the save below carries them.
  await dialog.getByRole('button', { name: 'Match the input closely' }).click();
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
          ).__savedToolOptions?.image?.['openai-inst-1']?.input_fidelity,
      ),
    )
    .toBe('high');
  await expect(dialog.getByText('Saved')).toBeVisible();
});

test('configures web search from the tools tab', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Config.SaveWebSearch':
          'async (req) => { globalThis.__savedWebSearch = req; }',
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Tools' }).click();

  // The card sits between the generation tools and the MCP list.
  await page.getByText('Web search', { exact: true }).click();
  // The settings page is itself a dialog; name the card's dialog.
  const dialog = page.getByRole('dialog', { name: 'Web search' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: /Brave/ }).click();
  await page.getByPlaceholder('Paste the provider API key').fill('bv-123');
  await dialog.getByRole('button', { name: 'Save & apply' }).click();
  const saved = await page.evaluate(
    () => (globalThis as { __savedWebSearch?: unknown }).__savedWebSearch,
  );
  expect(saved).toMatchObject({
    provider: 'brave',
    keys: { brave: 'bv-123' },
  });
});
