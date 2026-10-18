// The §6 baseline script (docs/plans/frontend-optimization.md), run against
// the built app with the renderer's performance probe switched on, and the
// samples written out as JSON.
//
// Why here and not by hand in the desktop app: a desktop reading mixes the
// machine's window server, its webview engine and whatever else the session
// was doing, so "before" and "after" a change can only be compared if both
// runs are the same script on the same page, and the plan asks for three
// samples each side. This is that script: one viewport, one transcript, one
// sequence of events and scrolls, a probe report at every phase. The engine
// is Chromium (Playwright), not WKWebView, so the absolute numbers are not
// the desktop app's — the shape of the cost and the before/after delta are
// what the review reads.
//
// It is a measurement harness, not a regression test: CI skips it. Run it
// with
//   OC_PERF_BASELINE=1 npx playwright test e2e/baseline.spec.ts --repeat-each=3
// and summarize the JSON it writes under test-results/ with
// scripts/perf-baseline.py.
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

const WS1 = '/Users/me/projects/one';
const WS2 = '/Users/me/projects/two';

// DELTA_GAP_MS is how long the script waits between two stream deltas. The
// store flushes visible text every 100ms (lib/stream.ts), so back-to-back
// emits collapse into one flush and the probe's flush_* columns would
// measure a paste, not a stream: ~25ms is the pace of a model writing tens
// of tokens a second, and it puts a few deltas in each flush window.
const DELTA_GAP_MS = 25;

type Sample = { name: string; value: number; labels: Record<string, string> };

// emit pushes one UI event through the mock's dispatch hook, the way the Go
// side streams a running turn.
async function emit(page: Page, data: unknown) {
  await page.evaluate(
    (d) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        d,
      ),
    data,
  );
}

const streamPart = (part: unknown) => ({
  type: 'stream',
  data: {
    run_id: 'r-1',
    conversation_id: 's-1',
    delta: { type: 'part', part },
  },
});

// workspaceHandlers make the workspace switch real: the app adopts a
// workspace when the backend announces it through "ready" (the config status
// carries work_dir), which is what Workspace.Open does here; the list and the
// active answer follow the same in-page variable. ChooseWorkspace throws, so
// the rail falls back to its path input — the product's own path when there
// is no native picker.
const workspaceHandlers = handlerSources({
  'Workspace.ChooseWorkspace': async () => {
    throw new Error('no native picker in the harness');
  },
  'Workspace.Open': async (workDir: string) => {
    const win = window as never as {
      __ws?: string;
      __emit?: (name: string, data: unknown) => void;
    };
    win.__ws = workDir;
    win.__emit?.('opencraft:ui', {
      type: 'ready',
      data: {
        needed: false,
        default_model: 'test/model',
        default_reasoning: true,
        work_dir: workDir,
        user_dir: '/user',
        version: '0.1.0-test',
        agents: 0,
      },
    });
  },
  'Workspace.Active': async () =>
    (window as never as { __ws?: string }).__ws ?? '/Users/me/projects/one',
  'Workspace.List': async () => [
    {
      id: 'ws-one',
      path: '/Users/me/projects/one',
      title: 'One',
      last_opened: '2026-01-02T00:00:00Z',
    },
    {
      id: 'ws-two',
      path: '/Users/me/projects/two',
      title: 'Two',
      last_opened: '2026-01-01T00:00:00Z',
    },
  ],
});

// turnsOf builds `count` settled turns of `each` messages: the history the
// script scrolls through. Hydration keeps the newest turns, so the script
// reads the tail.
function turnsOf(count: number, each: number) {
  return Array.from({ length: count }, (_, t) => ({
    seq: t + 1,
    at: '2026-01-01T00:00:00Z',
    artifacts: [],
    messages: Array.from({ length: each }, (_, m) => ({
      role: m === 0 ? 'user' : 'assistant',
      content: { parts: [{ type: 'text', text: `history ${t}-${m}` }] },
    })),
  }));
}

test('collects probe samples for the fixed script', async ({
  page,
}, testInfo) => {
  test.skip(
    !process.env.OC_PERF_BASELINE,
    'measurement harness: set OC_PERF_BASELINE=1 to run it',
  );
  const phases: Record<string, Sample[]> = {};

  // take reads the probe now: what it reports is the window since the last
  // report (frames, flushes, interactions), while the flush and markdown
  // percentiles are the page's ring, reported every window.
  const take = async (phase: string) => {
    const sent = await page.evaluate(async () => {
      const win = window as never as {
        __ocPerfProbeReport?: () => Promise<void>;
        __ocPerfReports?: unknown[][];
      };
      const before = win.__ocPerfReports?.length ?? 0;
      await win.__ocPerfProbeReport?.();
      return (win.__ocPerfReports ?? []).slice(before);
    });
    expect(sent.length, `probe report for ${phase}`).toBeGreaterThan(0);
    phases[phase] = sent.flat() as Sample[];
  };

  await page.setViewportSize({ width: 1440, height: 900 });
  // The probe reads this before the app boots (lib/perfProbe.ts).
  await page.addInitScript(() => {
    localStorage.setItem('oc.perfProbe', '1');
  });
  await page.addInitScript(mockBackend as never, {
    workspace: WS1,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    listSessions: [
      {
        id: 's-1',
        title: 'Working session',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 960,
        total_tokens: 0,
      },
    ],
    sessionTurns: turnsOf(40, 24),
    handlers: workspaceHandlers,
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Working session' }).click();
  expect(await page.getByText('history 39-23', { exact: true }).count()).toBe(
    1,
  );
  await take('open');

  // 1. One long answer: reasoning, then ~12000 characters of text in
  //    100-character deltas — the flush path the probe's flush_* series
  //    measure (F-01's 12000 appends, at the delta size a model sends).
  await typeComposerMessage(page, 'Write the migration plan');
  await page.getByRole('button', { name: 'Send' }).click();
  for (let i = 0; i < 40; i += 1) {
    await emit(
      page,
      streamPart({ type: 'reasoning', text: `weighing step ${i} ` }),
    );
    await page.waitForTimeout(DELTA_GAP_MS);
  }
  const chunk = 'lorem ipsum dolor sit amet '.repeat(4);
  for (let i = 0; i < 120; i += 1) {
    await emit(page, streamPart({ type: 'text', text: `${i} ${chunk}\n` }));
    await page.waitForTimeout(DELTA_GAP_MS);
  }
  await take('answer');

  // 2. A write_file of 4000 lines, as the tool card sees it: one call whose
  //    arguments carry the file (under MAX_TOOL_ARGS_CHARS), one result.
  const file = Array.from({ length: 4000 }, (_, i) => `line ${i}: written`);
  await emit(
    page,
    streamPart({
      type: 'tool_call',
      call: {
        id: 't-1',
        name: 'write_file',
        arguments: { file_path: 'docs/plan.md', content: file.join('\n') },
      },
    }),
  );
  await emit(
    page,
    streamPart({
      type: 'tool_result',
      result: {
        call_id: 't-1',
        is_error: false,
        content: { parts: [{ type: 'text', text: 'wrote docs/plan.md' }] },
      },
    }),
  );
  await emit(page, {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await expect(page.getByText('docs/plan.md').first()).toBeVisible();
  await take('write_file');

  // 3. History back and forth, two screens each way.
  const scroller = page.getByTestId('chat-scroll');
  for (const dir of [-1, 1]) {
    for (let i = 0; i < 2; i += 1) {
      await scroller.evaluate(
        (el, d) => el.scrollBy(0, d * el.clientHeight),
        dir,
      );
      await page.waitForTimeout(150);
    }
  }
  await take('scroll');

  // 4. One workspace switch, through the rail's own control: no native
  //    picker, so the path input; Open announces the new workspace, the app
  //    adopts it and the rail lists it.
  await page.getByRole('button', { name: 'Add workspace' }).click();
  const input = page.getByPlaceholder('/path/to/workspace');
  await input.fill(WS2);
  await input.press('Enter');
  await expect(page.getByRole('button', { name: 'Two' })).toBeVisible();
  await take('switch');

  const out = join(
    testInfo.outputDir,
    `perf-baseline-run${testInfo.repeatEachIndex}.json`,
  );
  mkdirSync(testInfo.outputDir, { recursive: true });
  writeFileSync(
    out,
    JSON.stringify({ run: testInfo.repeatEachIndex, phases }, null, 2),
  );
  test
    .info()
    .attach('perf-baseline', { path: out, contentType: 'application/json' });
});
