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

// TALL_STATE is the bytedance image vocabulary: ten knobs with dotted
// names, which is more than the settings panel is tall. The dialog it
// opens must stay inside the window anyway.
const TALL_STATE = {
  image: {
    instances: [
      {
        id: 'bytedance-sso-images',
        label: 'ByteDance',
        impl: 'bytedance',
        managed: false,
        fields: [
          {
            name: 'guidance_scale',
            kind: 'float',
            min: 0,
            exclusive_min: true,
          },
          { name: 'watermark', kind: 'bool' },
          {
            name: 'optimize_prompt.mode',
            kind: 'enum',
            values: ['standard', 'fast'],
          },
          {
            name: 'optimize_prompt.thinking',
            kind: 'enum',
            values: ['auto', 'enabled', 'disabled'],
          },
          { name: 'sequential', kind: 'bool' },
          { name: 'sequential_max_images', kind: 'int', min: 1, max: 15 },
          {
            name: 'size_token',
            kind: 'enum',
            values: ['1k', '1.5k', '2k', '3k', '4k', 'adaptive'],
          },
          { name: 'web_search', kind: 'bool' },
          { name: 'layer_decomposition', kind: 'bool' },
          {
            name: 'background',
            kind: 'enum',
            values: ['transparent', 'opaque'],
          },
        ],
        values: {},
        presets: [{ id: 'no_watermark', fields: { watermark: false } }],
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

test('keeps a tall provider vocabulary inside the window', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Config.ToolOptions': `async () => (${JSON.stringify(TALL_STATE)})`,
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Tools' }).click();
  await page.getByText('Image generation', { exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Image generation' });
  await expect(dialog).toBeVisible();
  // The dialog is taller than the settings panel it opens from, so the
  // panel used to clip its own header and its first rows away. Every
  // landmark has to be fully on screen, not merely in the DOM.
  await expect(
    dialog.getByRole('heading', { name: 'Image generation' }),
  ).toBeInViewport({ ratio: 0.9 });
  await expect(dialog.getByText('Guidance scale')).toBeInViewport({
    ratio: 0.9,
  });
  await expect(
    dialog.getByRole('button', { name: 'Save & apply' }),
  ).toBeInViewport();
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

test('configures the delegation policy from the tools tab', async ({
  page,
}) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Delegation.SaveDelegationSettings':
          'async (req) => { globalThis.__savedDelegation = req; }',
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Tools' }).click();

  await page.getByText('Delegation', { exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Delegation' });
  await expect(dialog).toBeVisible();

  await dialog.getByLabel('Concurrent delegations').fill('6');
  // One target from the registered list, one pattern typed by hand.
  await dialog.getByRole('button', { name: 'Allow assistant' }).click();
  const addFields = dialog.getByLabel('Add');
  await addFields.nth(1).fill('danger*');
  await addFields.nth(1).press('Enter');
  await dialog.getByRole('button', { name: 'Save & apply' }).click();

  const saved = await page.evaluate(
    () => (globalThis as { __savedDelegation?: unknown }).__savedDelegation,
  );
  expect(saved).toMatchObject({
    max_concurrency: 6,
    allowed_targets: ['assistant'],
    blocked_targets: ['danger*'],
  });
});
