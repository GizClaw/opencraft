// Usage tab regression coverage. The trend chart defaults to the
// all-models aggregate: a model renamed in settings keeps its old usage
// rows under the previous name, so defaulting to the all-time top model
// used to leave the chart blank while the table was full.
import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

const SUMMARY = [
  {
    model: 'deepseek-v4-flash',
    total_tokens: 161989893,
    input_tokens: 160206252,
    output_tokens: 1783641,
    cache_read_tokens: 156851328,
    cache_write_tokens: 0,
    reasoning_tokens: 1251298,
    latency_ms: 14119381,
    calls: 1910,
    workspaces: 3,
    sessions: 13,
    updated_at: '2026-09-10T06:21:19Z',
  },
  {
    model: 'deepseek-flash',
    total_tokens: 2392488,
    input_tokens: 2358509,
    output_tokens: 33979,
    cache_read_tokens: 2292608,
    cache_write_tokens: 0,
    reasoning_tokens: 24114,
    latency_ms: 169731,
    calls: 37,
    workspaces: 1,
    sessions: 2,
    updated_at: '2026-09-15T03:47:36Z',
  },
];

const AGGREGATE = [
  {
    time: '2026-09-09',
    input_tokens: 36826157,
    output_tokens: 588306,
    cache_read_tokens: 35753984,
    cache_write_tokens: 0,
    reasoning_tokens: 455316,
  },
  {
    time: '2026-09-10',
    input_tokens: 88548587,
    output_tokens: 606934,
    cache_read_tokens: 87210496,
    cache_write_tokens: 0,
    reasoning_tokens: 370754,
  },
  {
    time: '2026-09-15',
    input_tokens: 2349504,
    output_tokens: 33326,
    cache_read_tokens: 2283904,
    cache_write_tokens: 0,
    reasoning_tokens: 23799,
  },
];

// The two specs differ only in what the series binding returns, so the
// handler is built from inline source (Playwright cannot serialize a
// closure over the spec's variables).
function seriesHandler(points: unknown) {
  return `async (model) => {
    window.__seriesModel = model;
    return model === '' ? ${JSON.stringify(points)} : [];
  }`;
}

async function openUsageTab(page: import('@playwright/test').Page) {
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Usage' }).click();
}

test('defaults the trend to the all-models aggregate', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Config.ModelUsage': `async () => (${JSON.stringify(SUMMARY)})`,
        'Config.ModelUsageSessionCount': 'async () => 14',
        'Config.ModelUsageSeries': seriesHandler(AGGREGATE),
      },
    } as never,
  );
  await page.goto('/');
  await openUsageTab(page);

  await expect(page.getByRole('button', { name: 'All models' })).toBeVisible();
  await expect(page.locator('.recharts-area-area')).toHaveCount(5);
  await expect
    .poll(() =>
      page.evaluate(
        () => (window as never as { __seriesModel?: string }).__seriesModel,
      ),
    )
    .toBe('');
});

test('explains a range without usage instead of drawing a blank plot', async ({
  page,
}) => {
  await page.addInitScript(
    mockBackend as never,
    {
      handlers: {
        'Config.ModelUsage': `async () => (${JSON.stringify(SUMMARY)})`,
        'Config.ModelUsageSessionCount': 'async () => 14',
        'Config.ModelUsageSeries': seriesHandler([]),
      },
    } as never,
  );
  await page.goto('/');
  await openUsageTab(page);

  await expect(
    page.getByText('No usage recorded in this range.'),
  ).toBeVisible();
});
