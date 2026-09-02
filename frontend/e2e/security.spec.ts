import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

test('assistant output cannot inject HTML or javascript links', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'render something');
  await page.getByRole('button', { name: 'Send' }).click();

  await page.evaluate(() => {
    (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
      'opencraft:ui',
      {
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: {
            type: 'part',
            part: {
              type: 'text',
              text: '<script>window.pwned=1</script> [click](javascript:alert(1))',
            },
          },
        },
      },
    );
  });

  await expect
    .poll(() =>
      page.evaluate(() => (window as never as { pwned?: number }).pwned ?? 0),
    )
    .toBe(0);
  expect(
    await page.locator('script', { hasText: 'window.pwned' }).count(),
  ).toBe(0);

  // The javascript: destination is dropped entirely (no anchor).
  const hrefs = await page.$$eval('a', (as) =>
    as.map((a) => a.getAttribute('href')),
  );
  expect(hrefs.some((h) => h?.startsWith('javascript:'))).toBe(false);
});
