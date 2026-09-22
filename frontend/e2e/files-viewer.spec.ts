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

// The viewer's git surface: a chip in the toolbar for what happened to
// the file since HEAD, per-line marks in the gutter, and a silent
// reload when the file changes on disk under the open tab (the agent
// writing the file the reader is looking at).
test('marks the open file with its git changes and follows writes', async ({
  page,
}) => {
  const lines = (count: number) =>
    Array.from({ length: count }, (_, i) => `package files line ${i}`).join(
      '\n',
    );
  await page.addInitScript(mockBackend as never, {
    viewerFile: {
      path: '/tmp/w/internal/a.go',
      rel: 'internal/a.go',
      name: 'a.go',
      root: 'workspace',
      media_type: 'text/plain',
      size: 24,
      text: lines(60),
      mtime_ns: 100,
    },
    fileMarks: {
      in_repo: true,
      path: 'internal/a.go',
      kind: 'modified',
      staged: false,
      unstaged: true,
      untracked: false,
      unmerged: false,
      binary: false,
      truncated: false,
      additions: 2,
      deletions: 1,
      mtime_ns: 100,
      size: 24,
      adds: [{ start: 2, count: 1 }],
      mods: [{ start: 4, count: 1 }],
      dels: [{ after: 5, count: 1 }],
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
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await page.getByRole('link', { name: 'code' }).click();

  // The chip carries the kind letter and the line counts.
  const chip = page.getByRole('button', {
    name: 'Show the diff in the Git panel',
  });
  await expect(chip).toBeVisible();
  await expect(chip).toContainText('M');
  await expect(chip).toContainText('+2');
  await expect(chip).toContainText('−1');

  // One bar per changed line, plus a wedge where the deletion sits.
  await expect(page.locator('.oc-gm-add')).toHaveCount(1);
  await expect(page.locator('.oc-gm-mod')).toHaveCount(1);
  await expect(page.locator('.oc-gm-add')).toHaveAttribute(
    'data-tip',
    'Added line',
  );
  await expect(page.locator('.oc-gm-del-top')).toHaveCount(1);

  // A write under the open tab: the new payload arrives through the
  // artifact event and the body is swapped in place, no reopen.
  await page.evaluate(() => {
    const mock = (
      window as never as {
        __ocMockByModule: Record<string, Record<string, unknown>>;
      }
    ).__ocMockByModule;
    mock.File.ReadPreview = async () => ({
      path: '/tmp/w/internal/a.go',
      rel: 'internal/a.go',
      root: 'workspace',
      name: 'a.go',
      size: 30,
      media_type: 'text/plain',
      kind: 'text',
      text: 'package files line 0 rewritten\npackage files line 1',
      mtime_ns: 200,
    });
    mock.Git.FileMarks = async () => ({
      in_repo: true,
      path: 'internal/a.go',
      kind: 'modified',
      staged: false,
      unstaged: true,
      untracked: false,
      unmerged: false,
      binary: false,
      truncated: false,
      additions: 1,
      deletions: 0,
      mtime_ns: 200,
      size: 30,
      adds: [{ start: 1, count: 1 }],
    });
  });
  await emit({
    type: 'artifact',
    data: { conversation_id: 's-1', path: 'internal/a.go', bytes: 30 },
  });

  await expect(page.getByText('package files line 0 rewritten')).toBeVisible();
  await expect(page.locator('.oc-gm-add')).toHaveCount(1);
  await expect(page.locator('.oc-gm-mod')).toHaveCount(0);
});

// The tree's git rule: a folder carries the status of everything under
// it, so a collapsed directory already says that something inside it
// changed — the roll-up comes from the whole-repository snapshot, not
// from the rows the tree has listed.
test('rolls the change status up onto the folders of the tree', async ({
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
      text: 'package files',
    },
    gitStatus: {
      entries: [
        {
          path: 'internal/a.go',
          kind: 'modified',
          staged: false,
          unstaged: true,
          untracked: false,
          unmerged: false,
          directory: false,
          is_binary: false,
          additions: 4,
          deletions: 2,
          in_workspace: true,
        },
        {
          path: 'internal/scratch',
          kind: 'untracked',
          staged: false,
          unstaged: false,
          untracked: true,
          unmerged: false,
          directory: true,
          is_binary: false,
          additions: 0,
          deletions: 0,
          in_workspace: true,
        },
      ],
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
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await page.getByRole('link', { name: 'code' }).click();

  // The directory listing is the tree's own axis, so the spec hands it
  // over: the badges have to come from the git snapshot, not from the
  // listing.
  await page.evaluate(() => {
    const mock = (
      window as never as {
        __ocMockByModule: Record<string, Record<string, unknown>>;
      }
    ).__ocMockByModule;
    mock.File.List = async (dir: string) =>
      dir === '.'
        ? [
            { name: 'internal', path: 'internal', is_dir: true },
            { name: 'README.md', path: 'README.md', is_dir: false },
          ]
        : [
            { name: 'a.go', path: 'internal/a.go', is_dir: false },
            { name: 'scratch', path: 'internal/scratch', is_dir: true },
          ];
  });
  await page.getByRole('button', { name: 'Toggle file tree' }).click();

  // A collapsed folder already carries the aggregate of everything
  // under it, and the workspace row is the top-most folder.
  const folderBadge = page.locator('[data-tree-path="internal"] [data-kind]');
  await expect(folderBadge).toHaveAttribute('data-kind', 'modified');
  await expect(folderBadge).toHaveAttribute('data-files', '2');
  await expect(folderBadge).toHaveAttribute('data-mixed', 'true');
  await expect(
    page.locator('[data-tree-path="."] [data-kind]'),
  ).toHaveAttribute('data-files', '2');
  // A clean file draws nothing.
  await expect(
    page.locator('[data-tree-path="README.md"] [data-kind]'),
  ).toHaveCount(0);

  // Expanding the folder answers per directory, and a file inside the
  // collapsed untracked directory inherits its mark.
  await page.locator('[data-tree-path="internal"]').click();
  await expect(
    page.locator('[data-tree-path="internal/scratch"] [data-kind]'),
  ).toHaveAttribute('data-kind', 'untracked');
  await expect(
    page.locator('[data-tree-path="internal/a.go"] [data-kind]'),
  ).toHaveAttribute('data-kind', 'modified');
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
