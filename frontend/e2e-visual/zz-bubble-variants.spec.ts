// Throwaway design sheet (not part of the audit suite's baseline): renders
// the same user rows under four bubble treatments — the current one plus
// three proposals — so the choice can be made from pictures. Everything is
// injected at runtime (one <style> tag, and a DOM move for C); the app's own
// code is untouched, which is what makes the four columns comparable.
import { test, type Page } from '@playwright/test';
import { mockBackend } from '../e2e/mock/backend';

const SHOTS = 'e2e-visual/shots/bv';
const WS = '/Users/me/projects/opencraft';

type Variant = 'base' | 'a' | 'b' | 'c' | 'd';
const VARIANTS: Variant[] = ['base', 'a', 'b', 'c'];
// D is B's surface with C's structure: the quiet card holding the whole
// message, rendered on its own so the orthogonal pair can be judged.
// A screenshot-ish thumbnail stand-in: File.ReadAttachment is not mocked by
// the shared backend, and a resolved image is what the row actually shows.
const READ_ATTACHMENT = `async () => {
  const c = document.createElement('canvas');
  c.width = 520;
  c.height = 320;
  const g = c.getContext('2d');
  const grad = g.createLinearGradient(0, 0, 520, 320);
  grad.addColorStop(0, '#141a24');
  grad.addColorStop(1, '#1e2739');
  g.fillStyle = grad;
  g.fillRect(0, 0, 520, 320);
  g.fillStyle = '#4f8cff';
  g.fillRect(28, 30, 190, 9);
  g.fillStyle = 'rgba(230,233,239,0.5)';
  [78, 100, 122, 144, 166, 188].forEach((y, i) => g.fillRect(28, y, 360 - i * 34, 7));
  g.fillStyle = 'rgba(52,211,153,0.7)';
  g.fillRect(28, 224, 150, 7);
  g.fillStyle = 'rgba(79,140,255,0.3)';
  g.fillRect(28, 250, 300, 26);
  return {
    path: 'p',
    name: 'shot.png',
    media_type: 'image/png',
    data_url: c.toDataURL('image/png'),
  };
}`;

const VARIANT_CSS = `
/* A / C — same identity, much thinner: the tint stops competing with the
   answer, and the blocks inside stop borrowing assistant-only rules. */
html[data-bv='a'] div:has(> .user-bubble-md),
html[data-bv='c'] div:has(> .user-bubble-md) {
  background: color-mix(in srgb, var(--color-accent) 8%, transparent);
  border-color: color-mix(in srgb, var(--color-accent) 22%, var(--color-edge));
}
html[data-bv='a'] .user-bubble-md h1,
html[data-bv='a'] .user-bubble-md h2,
html[data-bv='a'] .user-bubble-md h3,
html[data-bv='c'] .user-bubble-md h1,
html[data-bv='c'] .user-bubble-md h2,
html[data-bv='c'] .user-bubble-md h3 {
  border-bottom: 0;
  padding-bottom: 0;
}
html[data-bv='a'] .user-bubble-md blockquote,
html[data-bv='c'] .user-bubble-md blockquote {
  border-left-color: color-mix(in srgb, var(--color-fg) 32%, transparent);
  background: color-mix(in srgb, var(--color-accent) 7%, transparent);
  color: var(--color-fg);
}
html[data-bv='a'] .user-bubble-md pre,
html[data-bv='c'] .user-bubble-md pre {
  background: color-mix(in srgb, var(--color-bg) 62%, transparent);
  border-color: color-mix(in srgb, var(--color-accent) 18%, var(--color-edge));
}
html[data-bv='a'] .user-bubble-md hr,
html[data-bv='c'] .user-bubble-md hr {
  border-top-color: color-mix(in srgb, var(--color-accent) 18%, var(--color-edge));
}
html[data-bv='a'] .user-bubble-md tbody tr:nth-child(even),
html[data-bv='c'] .user-bubble-md tbody tr:nth-child(even) {
  background: color-mix(in srgb, var(--color-accent) 6%, transparent);
}

/* B — the quiet card: identity moves from a fill to a rail on the reader's
   side (the mirror of the steer note's rail), and the rung is the app's own
   card surface instead of a tint over whatever is behind it. */
html[data-bv='b'] div:has(> .user-bubble-md),
html[data-bv='d'] div:has(> .user-bubble-md) {
  background: var(--color-panel2);
  border-color: var(--color-edge);
  border-right: 2px solid var(--color-accent);
  border-radius: var(--radius-card) 0 0 var(--radius-card);
}
html[data-bv='b'] .user-bubble-md :not(pre) > code,
html[data-bv='d'] .user-bubble-md :not(pre) > code {
  background: color-mix(in srgb, var(--color-accent) 12%, transparent);
  border-color: color-mix(in srgb, var(--color-accent) 25%, transparent);
  color: var(--color-accent);
}

/* C — A plus structure: the thumbnails and the file chip move inside the
   pill, so the tinted area is the whole message instead of its middle. */
html[data-bv='c'] div:has(> .user-bubble-md),
html[data-bv='d'] div:has(> .user-bubble-md) {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0.5rem;
}
html[data-bv='c'] div:has(> .user-bubble-md) > *,
html[data-bv='d'] div:has(> .user-bubble-md) > * {
  max-width: 100%;
}
html[data-bv='c'] div:has(> .user-bubble-md) img,
html[data-bv='d'] div:has(> .user-bubble-md) img {
  border-radius: var(--radius-control);
}
html[data-bv='c'] div:has(> .user-bubble-md) [aria-haspopup='menu'],
html[data-bv='d'] div:has(> .user-bubble-md) [aria-haspopup='menu'] {
  align-self: flex-start;
  background: color-mix(in srgb, var(--color-bg) 45%, transparent);
  border-color: color-mix(in srgb, var(--color-fg) 12%, transparent);
}
`;

async function applyVariant(page: Page, variant: Variant, light: boolean) {
  await page.evaluate(
    ({ variant, light, css }) => {
      const html = document.documentElement;
      html.dataset.bv = variant;
      if (light) html.classList.add('theme-light');
      else html.classList.remove('theme-light');
      let tag = document.getElementById('bv-style');
      if (!tag) {
        tag = document.createElement('style');
        tag.id = 'bv-style';
        document.head.appendChild(tag);
      }
      tag.textContent = css;
    },
    { variant, light, css: VARIANT_CSS },
  );
  if (variant !== 'c' && variant !== 'd') return;
  // C/D's structural half: the attachment cluster becomes one card.
  await page.evaluate(() => {
    const md = document.querySelector('.prose-chat.user-bubble-md');
    if (!md) return;
    const pill = md.parentElement;
    const wrapper = pill?.parentElement;
    if (!pill || !wrapper) return;
    const kids = Array.from(wrapper.children);
    const imgRow = kids.find((el) => el !== pill && el.querySelector('img'));
    const chip = kids.find(
      (el) =>
        el.tagName === 'BUTTON' && el.getAttribute('aria-haspopup') === 'menu',
    );
    if (imgRow instanceof HTMLElement) {
      imgRow.style.justifyContent = 'flex-start';
      pill.insertBefore(imgRow, pill.firstChild);
    }
    if (chip) pill.appendChild(chip);
  });
}

async function clipRect(page: Page, height: number) {
  return page.evaluate((h) => {
    const scroll = document
      .querySelector('[data-testid="chat-scroll"]')!
      .getBoundingClientRect();
    const row = document
      .querySelector('[data-msg-index]')!
      .getBoundingClientRect();
    return {
      x: Math.max(scroll.left, row.left - 22),
      y: Math.max(scroll.top + 2, row.top - 48),
      width: Math.min(scroll.right - row.left + 22, row.width + 44),
      height: h,
    };
  }, height);
}

function userTurn(parts: unknown[], reply: string) {
  return {
    seq: 1,
    at: '2026-01-01T00:00:00Z',
    artifacts: [],
    messages: [
      { role: 'user', content: { parts } },
      {
        role: 'assistant',
        content: { parts: [{ type: 'text', text: reply }] },
      },
    ],
  };
}

async function sheet(
  page: Page,
  session: string,
  turns: unknown[],
  height: number,
  variants: Variant[] = VARIANTS,
) {
  await page.setViewportSize({ width: 1568, height: 980 });
  await page.addInitScript(
    mockBackend as never,
    {
      workspace: WS,
      handlers: { 'File.ReadAttachment': READ_ATTACHMENT },
      listSessions: [
        {
          id: 's-bv',
          title: session,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
          messages: 2,
          total_tokens: 0,
        },
      ],
      sessionTurns: turns,
    } as never,
  );
  const theme = process.env.BV_THEME === 'light' ? 'light' : 'dark';
  const key = process.env.BV_KEY ?? 'sheet';
  for (const variant of variants) {
    await page.goto('/');
    await page.getByRole('button', { name: session }).click();
    await page.waitForTimeout(500);
    // Park the pointer: the sidebar row under the cursor pops its own
    // hover card into the transcript's corner.
    await page.mouse.move(1520, 960);
    await applyVariant(page, variant, theme === 'light');
    await page.waitForTimeout(150);
    const clip = await clipRect(page, height);
    await page.screenshot({
      path: `${SHOTS}/${key}-${variant}-${theme}.png`,
      clip,
    });
  }
}

// 1 — the whole cluster: a short line, one screenshot, two files.
test('user row: text + image + files', async ({ page }) => {
  process.env.BV_KEY = '1-cluster';
  await sheet(
    page,
    'Bubble case',
    [
      userTurn(
        [
          {
            type: 'text',
            text: '这两个方案的截图和 spec 都在附件里，先对齐一下范围。',
          },
          {
            type: 'image',
            source: {
              kind: 'url',
              url: '/Users/me/projects/opencraft/tmp/prototype.png',
              media_type: 'image/png',
            },
          },
          {
            type: 'file',
            uri: '/Users/me/projects/opencraft/docs/rollout-plan.md',
            name: 'rollout-plan.md',
            media_type: 'text/markdown',
          },
          {
            type: 'file',
            uri: '/Users/me/projects/opencraft/tmp/bubble-geometry.md',
            name: 'bubble-geometry.md',
            media_type: 'text/markdown',
          },
        ],
        '范围我按截图里的第二个方案写了，spec 里补了一节几何约束。',
      ),
    ],
    420,
    [...VARIANTS, 'd'],
  );
});

// 2 — rich Markdown inside the bubble: heading, list, quote, inline code,
// fenced block. This is the surface where a loud tint hurts most.
test('user row: rich markdown', async ({ page }) => {
  process.env.BV_KEY = '2-markdown';
  await sheet(
    page,
    'Markdown bubble',
    [
      userTurn(
        [
          {
            type: 'text',
            text: [
              '## Usage units',
              '',
              '- keep the range picker sticky',
              '- move `formatCompact` into `lib/`',
              '',
              '> fold anything past 999 into the next unit',
              '',
              '```ts',
              'formatCompact(4_739_140_000) // 4.74B',
              '```',
            ].join('\n'),
          },
        ],
        'On it.',
      ),
    ],
    430,
  );
});

// 3 — a pasted brief: what a long user message does to a tinted pill.
test('user row: pasted brief', async ({ page }) => {
  process.env.BV_KEY = '3-brief';
  await sheet(
    page,
    'Pasted brief',
    [
      userTurn(
        [
          {
            type: 'text',
            text: [
              '把会话导入的这条路径再收一遍：',
              '',
              '1. `import` 现在先写 session store，再补 usage，两步之间崩了就会留下半条记录；',
              '2. 新的 workspace 迁移要走 `host.Manager`，不要在前端拼路径；',
              '3. 做完跑 `go test ./internal/capabilities/sessions/...` 和一遍 e2e。',
              '',
              '另外顺手确认一下 rollout 的序列化还是 `jsonl`，别换成 `sqlite`。',
              '',
              '字段对齐 [docs/adr/0007-rollout.md](https://example.com/0007) 里的这张表：',
              '',
              '| 字段 | 现在 | 目标 |',
              '| --- | --- | --- |',
              '| `seq` | int | int |',
              '| `at` | string | unix ms |',
              '',
              '---',
              '',
              '就这些。',
            ].join('\n'),
          },
        ],
        '收到，我先把导入的两次写入合成一个事务，再跑那两套测试。',
      ),
    ],
    660,
  );
});
