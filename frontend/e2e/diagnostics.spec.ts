import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

// The diagnostics tab keeps its heavy tools in dialogs, portaled out of the
// settings panel (which clips its own overflow). The charts dialog draws a
// full grid of recharts lines, and recharts reports its container's scale
// on every frame — the animation had to go, or opening the dialog took the
// UI down with React error #185.
test('opens the diagnostics dialogs without crashing the UI', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (err) => errors.push(err.message));

  await page.addInitScript(mockBackend as never, {});
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click();

  await page.getByRole('button', { name: 'Open charts' }).click();
  const charts = page.getByRole('dialog', { name: 'Performance' });
  await expect(charts).toBeVisible();
  // The grid actually drew: an empty answer would hide the crash path.
  await expect(charts.locator('svg.recharts-surface').first()).toBeVisible();
  // The renderer probe's series are charted next to the web-vitals ones;
  // they live in their own section because they are per-30s-window stats.
  // Exact, because the copy of another section's hint mentions the probe:
  // a substring match would resolve to both.
  await expect(charts.getByText('Renderer probe', { exact: true })).toBeVisible();
  // The grid is taller than the dialog: the body has to scroll, or half
  // the charts are unreachable behind the panel's overflow.
  const body = charts.locator('div.overflow-y-auto').first();
  const scroll = await body.evaluate((el) => ({
    overflow: el.scrollHeight - el.clientHeight,
    moved: (() => {
      el.scrollTop = 240;
      return el.scrollTop;
    })(),
  }));
  expect(scroll.overflow).toBeGreaterThan(50);
  expect(scroll.moved).toBeGreaterThan(50);
  await body.evaluate((el) => {
    el.scrollTop = 0;
  });
  // A wheel over a chart has to reach the scroller: the pointer sits on
  // an svg the chart library owns, not on the panel's own padding.
  const chart = charts.locator('svg.recharts-surface').nth(1);
  const box = (await chart.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, 320);
  // Wheel scrolling is delivered asynchronously; poll instead of sampling
  // the position once.
  await expect
    .poll(() => body.evaluate((el) => el.scrollTop))
    .toBeGreaterThan(50);
  // A dialog nested inside the settings panel is clipped by it; the
  // settings dialog must not be an ancestor.
  await expect(
    page.locator('[aria-labelledby="settings-title"] [role="dialog"]'),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(charts).toBeHidden();

  await page.getByRole('button', { name: 'Open log' }).click();
  await expect(
    page.getByRole('dialog', { name: 'Application log' }),
  ).toBeVisible();
  await page.keyboard.press('Escape');

  // The mock's Environment binding is absent, which surfaces as an
  // unrelated boot error; anything else is ours.
  expect(errors.filter((e) => !e.includes("reading 'OS'"))).toEqual([]);
});
