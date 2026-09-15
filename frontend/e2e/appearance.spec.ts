// Settings > Interface appearance: the interface/code font and the text size
// are stored through the desktop preference document, the font picker lists
// the families the host reports, and the localStorage mirror paints the first
// frame before the binding resolves.
import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

async function openInterfaceTab(page: Page) {
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Interface' }).click();
}

function cssVar(page: Page, name: string) {
  return page.evaluate(
    (varName) => document.documentElement.style.getPropertyValue(varName),
    name,
  );
}

test('picks a host font family and stores it', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  await openInterfaceTab(page);

  await page.getByRole('button', { name: 'Interface font' }).click();
  await page.getByLabel('Search fonts').fill('pingfang');
  await page.getByRole('option', { name: 'PingFang SC' }).click();

  expect(await cssVar(page, '--oc-font-sans')).toContain('PingFang SC');

  // Escape closes the menu, not the settings dialog underneath it.
  await page.getByRole('button', { name: 'Interface font' }).click();
  await page.keyboard.press('Escape');
  await expect(page.getByLabel('Search fonts')).toBeHidden();
  await expect(page.getByRole('tab', { name: 'Interface' })).toBeVisible();

  const calls = await page.evaluate(
    () =>
      (
        window as unknown as {
          __ocUISettingsCalls?: {
            fontFamily?: string;
            fontFamilyName?: string;
          }[];
        }
      ).__ocUISettingsCalls ?? [],
  );
  expect(calls.at(-1)).toMatchObject({
    fontFamily: 'custom',
    fontFamilyName: 'PingFang SC',
  });

  // The text-size control (small A, notches, large A) scales the root font
  // size, which every rem-based size in the app follows.
  await page.getByRole('button', { name: 'Larger text' }).click();
  expect(await cssVar(page, '--oc-root-font-size')).toBe('17.50px');
  await page.getByRole('slider', { name: 'Text size' }).press('ArrowRight');
  expect(await cssVar(page, '--oc-root-font-size')).toBe('19.60px');
});

test('reports an unavailable catalogue instead of offering a text field', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, { fontFamilies: [] } as never);
  await page.goto('/');
  await openInterfaceTab(page);

  await page.getByRole('button', { name: 'Interface font' }).click();
  await expect(page.getByText(/does not expose its font list/)).toBeVisible();
  // Only the system preset is selectable; the search box is the only text
  // input (the chat composer is a contenteditable).
  await expect(page.getByRole('option')).toHaveCount(1);
  await expect(page.locator('input[type="text"]')).toHaveCount(1);
});

test('keeps the picker open while its list is scrolled', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      fontFamilies: Array.from(
        { length: 60 },
        (_, i) => `Mock Font ${String(i).padStart(2, '0')}`,
      ),
    } as never,
  );
  await page.goto('/');
  await openInterfaceTab(page);
  await page.getByRole('button', { name: 'Interface font' }).click();

  const list = page.getByRole('listbox');
  await list.hover();
  await page.mouse.wheel(0, 320);

  // The wheel scrolled the list itself, and the menu stayed open around it.
  await expect
    .poll(() => list.evaluate((node) => node.scrollTop))
    .toBeGreaterThan(0);
  await expect(list).toBeVisible();

  await page.getByRole('option', { name: 'Mock Font 20' }).click();
  expect(await cssVar(page, '--oc-font-sans')).toContain('Mock Font 20');
});

test('paints a stored font and size on launch', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: handlerSources({
        'Lifecycle.GetUISettings': async () => ({
          fontFamily: 'custom',
          fontFamilyName: 'LXGW WenKai',
          codeFont: 'custom',
          codeFontName: 'Fira Code',
          fontScale: 1.4,
        }),
      }),
    } as never,
  );
  await page.goto('/');

  await expect.poll(() => cssVar(page, '--oc-root-font-size')).toBe('19.60px');
  expect(await cssVar(page, '--oc-font-sans')).toContain('LXGW WenKai');
  expect(await cssVar(page, '--oc-font-mono')).toContain('Fira Code');
});
