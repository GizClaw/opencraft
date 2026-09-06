import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

// Clicking a local markdown link must open the file viewer tab instead
// of navigating the webview; clicking an http(s) link must hand the URL
// to the system browser and keep the app on its own origin.
test('routes local links to the file viewer and http links outside', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    viewerFile: {
      path: '/tmp/w/internal/a.go',
      rel: 'internal/a.go',
      name: 'a.go',
      root: 'workspace',
      media_type: 'text/plain',
      size: 24,
      text: Array.from(
        { length: 300 },
        (_, i) => `package files line ${i}`,
      ).join('\n'),
    },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'open file');
  await page.getByRole('button', { name: 'Send' }).click();

  const emit = (data: unknown) =>
    page.evaluate(
      (d) =>
        (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
          'opencraft:ui',
          d,
        ),
      data,
    );

  await emit({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: { type: 'text', text: 'click [code](internal/a.go)' },
      },
    },
  });

  await emit({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'text',
          text: ' and [web](https://example.com)',
        },
      },
    },
  });
  await emit({
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });

  // Markdown renders once the turn completes; both links are real
  // anchors at this point.
  await page.getByRole('link', { name: 'code' }).click();

  await expect(page.getByText('package files line 0')).toBeVisible();
  await expect(page.getByText('a.go').first()).toBeVisible();
  await expect(page).toHaveURL('/');

  // CodeMirror keeps its own scroller: the code viewport must scroll
  // internally instead of trapping the wheel.
  const scroller = page.locator('.cm-scroller');
  await expect(scroller).toBeVisible();
  const before = await scroller.evaluate((el) => el.scrollTop);
  await scroller.hover();
  await page.mouse.wheel(0, 600);
  await page.waitForTimeout(150);
  const after = await scroller.evaluate((el) => el.scrollTop);
  expect(after).toBeGreaterThan(before);

  await page.getByRole('link', { name: 'web' }).click();

  await expect
    .poll(() =>
      page.evaluate(
        () => (globalThis as never as { __extUrl?: string }).__extUrl,
      ),
    )
    .toBe('https://example.com');
  await expect(page).toHaveURL('/');
});

// Regression: on a fresh conversation the composer stays vertically
// centered. The FileViewer layout wrapper must not collapse the chat
// column height (which used to push the composer to the top).
test('keeps the empty-session composer centered', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {});
  await page.goto('/');

  const box = (await page.locator('.ProseMirror').boundingBox()) ?? null;
  expect(box).not.toBeNull();
  if (box) {
    const centerY = box.y + box.height / 2;
    const viewport = page.viewportSize();
    expect(viewport).not.toBeNull();
    if (viewport) {
      expect(centerY).toBeGreaterThan(viewport.height * 0.3);
      expect(centerY).toBeLessThan(viewport.height * 0.7);
    }
  }
});
