// The ⌘K palette is the one surface a keyboard user starts from, so it
// has to open with the caret already in its search field — and, before
// anything else, the keystrokes have to go into that field instead of
// the composer that held focus a moment ago.
import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

test('⌘K opens the palette with the caret in its search field', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  // The shortcut listener installs with the shell, so the composer on
  // screen means the key is heard.
  const composer = page.locator('.ProseMirror').first();
  await composer.waitFor();
  // A keyboard user opens the palette mid-draft, with the caret in the
  // composer.
  await composer.click();
  await expect(composer).toBeFocused();

  await page.keyboard.press('ControlOrMeta+k');
  const search = page.getByRole('combobox');
  await expect(search).toBeFocused();

  await page.keyboard.type('theme');
  await expect(search).toHaveValue('theme');
  await expect(page.getByRole('option').first()).toBeVisible();

  // Escape closes the palette and focus goes back where it was.
  await page.keyboard.press('Escape');
  await expect(search).toHaveCount(0);
  await expect(composer).toBeFocused();
});

test('the surface a palette command opens takes the keyboard', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  const composer = page.locator('.ProseMirror').first();
  await composer.waitFor();
  await composer.click();

  await page.keyboard.press('ControlOrMeta+k');
  await page.keyboard.type('usage');
  await page.keyboard.press('Enter');

  // Closing the palette and opening the settings page land in one
  // commit; the keyboard goes to the surface the command opened, not
  // back to the composer.
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(() => {
        const panel = document.querySelector('[role="dialog"]');
        return panel !== null && panel.contains(document.activeElement);
      }),
    )
    .toBe(true);
});
