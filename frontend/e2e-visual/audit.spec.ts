// UI audit harness: renders the real frontend against the mock backend
// and dumps one screenshot per surface (dark and light) so a design
// change can be reviewed as before/after images instead of a diff of
// class strings.
//
// Run with `npm run audit:ui --prefix frontend`; it is a separate
// Playwright config (playwright.visual.config.ts), so the CI e2e job
// (testDir: e2e) never picks it up. Screenshots land in
// e2e-visual/shots/ and are gitignored.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from '../e2e/mock/backend';
import { typeComposerMessage } from '../e2e/helpers';

const SHOTS = 'e2e-visual/shots';
const WS = '/Users/me/projects/opencraft';

type Emit = (name: string, data: unknown) => Promise<void>;

const emitter =
  (page: Page): Emit =>
  (name, data) =>
    page.evaluate(
      ([n, d]) =>
        (
          window as never as {
            __emit: (name: string, data: unknown) => void;
          }
        ).__emit(n as string, d),
      [name, data] as const,
    );

// The chat header strip: the sidebar's macOS traffic-light row plus the
// chat pane's own title bar, both 44px, plus the first rows underneath so
// the two hairline seams are in frame. Clipped from the window top at 2x,
// because the strip is where hairlines and micro type have to hold up.
async function headerShot(page: Page, name: string) {
  await page.screenshot({
    path: `${SHOTS}/${name}.png`,
    clip: { x: 0, y: 0, width: 1568, height: 104 },
  });
}

const USAGE_SUMMARY = [
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
    model: 'gpt-5.2-codex',
    total_tokens: 23924888,
    input_tokens: 23585090,
    output_tokens: 339798,
    cache_read_tokens: 22926080,
    cache_write_tokens: 1200,
    reasoning_tokens: 241140,
    latency_ms: 1697310,
    calls: 137,
    workspaces: 1,
    sessions: 12,
    updated_at: '2026-09-15T03:47:36Z',
  },
];

const USAGE_AGGREGATE = [
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

async function openSettings(page: Page) {
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${SHOTS}/${name}.png` });
}

test('welcome', async ({ page }) => {
  await page.addInitScript(mockBackend as never, { workspace: WS });
  await page.goto('/');
  await page.waitForTimeout(400);
  await shot(page, '01-welcome');
});

test('settings dark', async ({ page }) => {
  await page.addInitScript(mockBackend as never, { workspace: WS });
  await page.goto('/');
  await openSettings(page);
  await page.waitForTimeout(500);
  const tabs: [string, string][] = [
    ['General', '10-settings-general'],
    ['Interface', '11-settings-interface'],
    ['Inference', '12-settings-inference'],
    ['Tools', '13-settings-tools'],
    ['Memory', '14-settings-memory'],
    ['Permissions', '15-settings-permissions'],
    ['Diagnostics', '16-settings-diagnostics'],
    ['Import', '17-settings-import'],
  ];
  for (const [tab, name] of tabs) {
    await page.getByRole('tab', { name: tab }).click();
    await page.waitForTimeout(700);
    await shot(page, name);
  }
});

// The generation-tool dialog is the shared Modal shell (header, scroll
// body, SaveBar footer), so it is the surface to re-check after any
// change to that shell.
test('tools dialog', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      workspace: WS,
      handlers: {
        // Same shape as the tools e2e fixture: the dialog renders one
        // row per configured instance, with driver fields and a preset.
        'Config.ToolOptions': `async () => (${JSON.stringify({
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
                  {
                    name: 'output_compression',
                    kind: 'int',
                    min: 0,
                    max: 100,
                  },
                ],
                values: { background: 'transparent' },
                presets: [
                  { id: 'edit_fidelity', fields: { input_fidelity: 'high' } },
                ],
              },
            ],
          },
          video: { instances: [] },
        })})`,
      },
    } as never,
  );
  await page.goto('/');
  await openSettings(page);
  await page.getByRole('tab', { name: 'Tools' }).click();
  await page.waitForTimeout(500);
  await page.getByText('Image generation', { exact: true }).click();
  await page.waitForTimeout(500);
  await shot(page, '18-tools-dialog');
});

test('settings light', async ({ page }) => {
  await page.addInitScript(mockBackend as never, { workspace: WS });
  await page.goto('/');
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await openSettings(page);
  await page.waitForTimeout(500);
  for (const [tab, name] of [
    ['Tools', '20-tools-light'],
    ['Memory', '21-memory-light'],
    ['Diagnostics', '22-diagnostics-light'],
    ['Inference', '23-inference-light'],
  ] as [string, string][]) {
    await page.getByRole('tab', { name: tab }).click();
    await page.waitForTimeout(700);
    await shot(page, name);
  }
});

test('usage with data', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    // Without these three the tab renders its empty state and the shot
    // proves nothing about the hero card or the trend chart.
    handlers: {
      'Config.ModelUsage': `async () => (${JSON.stringify(USAGE_SUMMARY)})`,
      'Config.ModelUsageSessionCount': 'async () => 25',
      'Config.ModelUsageSeries': `async () => (${JSON.stringify(
        USAGE_AGGREGATE,
      )})`,
    },
    listSessions: [
      {
        id: 's-1',
        workspace: WS,
        title: 'Wire up usage page',
        updated_at: '2026-09-15T03:47:36Z',
        status: 'idle',
      },
    ],
  });
  await page.goto('/');
  await openSettings(page);
  await page.getByRole('tab', { name: 'Usage' }).click();
  await page.waitForTimeout(900);
  await shot(page, '30-usage');
});

test('chat transcript', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    // The activity card's process section reads Session.Processes; one
    // running dev server shows the whole card (plan, thought, output).
    processes: [
      {
        process_id: 'p-1',
        argv: ['npm', 'run', 'dev'],
        workdir: WS,
        tty: false,
        pid: 4242,
        started_at: '2026-01-01T00:00:00Z',
        running: true,
        tail: 'VITE v7.0.0  ready in 412 ms\n\n  ➜  Local:   http://localhost:5173/\n  ➜  Network: http://192.168.1.24:5173/\n',
        truncated: false,
        seq: 96,
      },
    ],
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Add the usage hero card');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'text',
          text: 'Heads up: **this is the first line** of ',
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        // The plan card is an overlay pinned to this corner, so the
        // transcript's first line runs underneath it by design.
        part: { type: 'text', text: 'the reply the plan card floats over.\n' },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: {
            id: 'call-1',
            name: 'exec_command',
            arguments: { command: 'npm run build --prefix frontend' },
          },
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_result',
          result: {
            call_id: 'call-1',
            content: {
              parts: [
                {
                  type: 'text',
                  text: '{"exit_code":0,"stdout":"vite v7 building…","stderr":""}',
                },
              ],
            },
            is_error: false,
          },
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: {
            id: 'call-plan',
            name: 'update_plan',
            arguments: {
              plan: [
                { step: 'Rework the usage hero', status: 'completed' },
                { step: 'Wire the range picker', status: 'in_progress' },
                { step: 'Run the e2e suite', status: 'pending' },
              ],
            },
          },
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        // The activity card's thought section.
        part: {
          type: 'reasoning',
          text: 'The hero card already owns the top of the page, so the range picker moves under it instead of beside it; keeping both in one row would squeeze the sparkline below its legibility floor.',
        },
      },
    },
  });
  // The thought is the last thing on the wire, so the card's section is
  // live at shot time (expanded, "Thinking…") instead of folded away by
  // whatever the model did next.
  await page.waitForTimeout(400);
  await shot(page, '40-chat');
  // The card is the surface here that is new in both themes: the plan
  // tints, the section glyphs and the process tail all have to survive
  // the flipped palette.
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await page.waitForTimeout(200);
  await shot(page, '41-chat-light');
});

// The activity card's whole lifetime: it is in the corner while the turn
// or one of the conversation's processes runs, and the corner is the
// transcript's again once neither does. The pair is what makes that
// reviewable as images — nothing on screen takes the card down.
test('activity card lifetime', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
    processes: [
      {
        process_id: 'p-1',
        argv: ['npm', 'run', 'dev'],
        workdir: WS,
        tty: false,
        pid: 4242,
        started_at: '2026-01-01T00:00:00Z',
        running: true,
        tail: `VITE v7.0.0  ready in 412 ms\n\n  ➜  Local:   http://localhost:5173/\n`,
        truncated: false,
        seq: 96,
      },
    ],
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Move the usage recorder out of the desktop');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: {
            id: 'call-plan',
            name: 'update_plan',
            arguments: {
              plan: [
                {
                  step: 'Move the recorder into the host',
                  status: 'completed',
                },
                { step: 'Keep the per-workspace split', status: 'in_progress' },
                { step: 'Run the host tests', status: 'pending' },
              ],
            },
          },
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'reasoning',
          text: 'The recorder belongs to the host: the desktop shell must not open a second one, and a per-workspace split keeps usage attributable…',
        },
      },
    },
  });
  await page.waitForTimeout(400);
  await shot(page, '41b-activity-card-live');

  // Folded to its header — the card's one control. The dot and the title
  // stay (the overlay is still reporting the turn), the body and the
  // card's column are gone.
  await page.getByTestId('activity-card-header').click();
  await expect(page.getByTestId('activity-card')).toHaveAttribute(
    'data-folded',
    'true',
  );
  await shot(page, '41d-activity-card-folded');
  await page.getByTestId('activity-card-header').click();

  // The dev server exits while the turn runs: a stopped process is not
  // activity, so its row and its tail leave the card at the next read.
  // The card stays up because the turn, the plan and the thought are.
  await page.evaluate(() => {
    const modules = (
      window as never as {
        __ocMockByModule: Record<string, Record<string, unknown>>;
      }
    ).__ocMockByModule;
    modules.Session.Processes = async () => [
      {
        process_id: 'p-1',
        argv: ['npm', 'run', 'dev'],
        workdir: '/Users/me/projects/opencraft',
        tty: false,
        pid: 4242,
        started_at: '2026-01-01T00:00:00Z',
        running: false,
        exit_code: 0,
        exit_reason: 'exited',
        tail: 'VITE v7.0.0  ready in 412 ms\n\n  ➜  Local:   http://localhost:5173/\n',
        truncated: false,
        seq: 128,
      },
    ];
  });
  await expect(page.getByTestId('process-section')).toHaveCount(0);
  await expect(page.getByTestId('activity-card')).toBeVisible();

  // The turn ends, and with nothing running the card's lifetime is over:
  // the corner goes back to the transcript.
  await emit('opencraft:ui', {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await expect(page.getByTestId('activity-card')).toHaveCount(0);
  await page.waitForTimeout(300);
  await shot(page, '41c-activity-card-after-work');
});

test('chat interactions', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Run the release script');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForTimeout(300);
  const emit = emitter(page);
  const spec = (
    id: string,
    severity: string,
    title: string,
    kind: string,
    body: string,
    options: { label: string; value: string }[],
  ) => ({
    id,
    run_id: 'r-1',
    conversation_id: 's-1',
    kind,
    title,
    body: [{ type: 'text', text: body }],
    options,
    multi: false,
    allow_other: false,
    source: 'test',
    severity,
  });
  await emit('opencraft:ui', {
    type: 'interact',
    data: spec(
      'p-1',
      'info',
      'Which rollout strategy?',
      'select',
      'Pick how the next deploy should proceed.',
      [
        { label: 'Rolling update', value: 'rolling' },
        { label: 'Blue / green', value: 'bluegreen' },
      ],
    ),
  });
  await emit('opencraft:ui', {
    type: 'interact',
    data: spec(
      'p-2',
      'notice',
      'Allow running npm publish?',
      'select',
      'Command is not in the sandbox allowlist: npm publish',
      [
        { label: 'Allow once', value: 'allow_once' },
        { label: 'Deny', value: 'deny' },
        { label: 'Always allow', value: 'always' },
      ],
    ),
  });
  await emit('opencraft:ui', {
    type: 'interact',
    data: spec(
      'p-3',
      'danger',
      'Run outside the sandbox?',
      'select',
      'The sandbox refused this command. Approving re-runs the whole command on the host with full access.',
      [
        { label: 'Run outside the sandbox (once)', value: 'escalate_once' },
        { label: 'Deny', value: 'deny' },
      ],
    ),
  });
  await page.waitForTimeout(300);
  await shot(page, '41-interactions');
});

test('files empty with tree', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [
      {
        id: 's-1',
        workspace: WS,
        title: 'Browse the workspace',
        updated_at: '2026-09-15T03:47:36Z',
        status: 'idle',
      },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Show / hide file viewer' }).click();
  await page.getByRole('button', { name: 'Browse workspace' }).click();
  await page.waitForTimeout(700);
  await shot(page, '50-files');
});

test('automations', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    automations: [
      {
        id: 't-1',
        name: 'Daily brief',
        prompt: 'summarize the repo status',
        schedule: { type: 'daily', time: '09:00' },
        workspace: WS,
        mode: 'workspace',
        model: '',
        think: 'medium',
        conversation_id: '',
        notify: 'always',
        enabled: true,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        last_run_at: '',
        last_status: '',
        next_run_at: '',
      },
      {
        id: 't-2',
        name: 'Weekly triage',
        prompt: 'file the new issues',
        schedule: { type: 'weekly', day: 'mon', time: '09:00' },
        workspace: WS,
        mode: 'workspace',
        model: '',
        think: 'medium',
        conversation_id: '',
        notify: 'always',
        enabled: true,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        last_run_at: '2026-09-14T09:00:00Z',
        last_status: 'completed',
        next_run_at: '2026-09-21T09:00:00Z',
      },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Automations' }).click();
  await page.waitForTimeout(700);
  await shot(page, '60-automations');
});

// The apply_patch card is the densest tool surface in the transcript:
// header summary, per-file diff headers, and code that must stay inside
// the card however long a line is. The fixture deliberately carries a
// line far wider than the card (and one >200 char comment) so the shot
// proves wrap instead of sideways scroll.
const PATCH_TEXT = [
  '*** Begin Patch',
  '*** Update File: frontend/src/lib/store.ts',
  '@@ -582,7 +582,9 @@',
  ' // mergeTurnDoc appends a produced file, or refreshes its byte count',
  '-function mergeTurnDoc(docs: TurnDoc[], path: string, bytes: number) {',
  '+function mergeTurnDoc(',
  '+  docs: TurnDoc[], path: string, bytes: number, note: string,',
  '+) {',
  '*** Add File: frontend/src/lib/seq.ts',
  '+export const nextSeq = (counter: { value: number }) => ++counter.value;',
  '*** Delete File: frontend/src/lib/legacy.ts',
  '*** End Patch',
].join('\n');

const PATCH_FILES = [
  {
    path: 'frontend/src/lib/store.ts',
    action: 'update',
    added: 3,
    removed: 1,
    lines: [
      {
        kind: 'context',
        old_num: 582,
        new_num: 582,
        text: '  // mergeTurnDoc appends a produced file, or refreshes its byte count',
      },
      {
        kind: 'delete',
        old_num: 583,
        new_num: 0,
        text: '  function mergeTurnDoc(docs: TurnDoc[], path: string, bytes: number) {',
      },
      {
        kind: 'add',
        old_num: 0,
        new_num: 583,
        text: '  // A comment that runs well past the width of the chat card on purpose, so the audit shot shows long diff lines wrapping under the code column instead of scrolling the card sideways.',
      },
      {
        kind: 'add',
        old_num: 0,
        new_num: 584,
        text: '  function mergeTurnDoc(',
      },
      {
        kind: 'add',
        old_num: 0,
        new_num: 585,
        text: '    docs: TurnDoc[], path: string, bytes: number, note: string,',
      },
      { kind: 'context', old_num: 584, new_num: 586, text: '  ) {' },
    ],
  },
  {
    path: 'frontend/src/lib/seq.ts',
    action: 'add',
    added: 1,
    removed: 0,
    lines: [
      {
        kind: 'add',
        old_num: 0,
        new_num: 1,
        text: 'export const nextSeq = (counter: { value: number }) => ++counter.value;',
      },
    ],
  },
  {
    path: 'frontend/src/lib/legacy.ts',
    action: 'delete',
    added: 0,
    removed: 2,
    lines: [
      {
        kind: 'delete',
        old_num: 1,
        new_num: 0,
        text: 'export const legacy = 1;',
      },
      {
        kind: 'delete',
        old_num: 2,
        new_num: 0,
        text: 'export const stale = 2;',
      },
    ],
  },
];

test('apply patch card', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      workspace: WS,
      startTurn: { run_id: 'r-1', context_id: 's-1' },
      handlers: {
        // The Go binding renders the patch against the workspace; the
        // audit runs without one, so the fixture supplies the DTOs.
        'File.RenderPatch': `async () => (${JSON.stringify(PATCH_FILES)})`,
      },
    } as never,
  );
  await page.goto('/');
  await typeComposerMessage(page, 'Tidy up the merge helper');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: { type: 'text', text: 'Refactoring the diff merge path:\n' },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: {
            id: 'call-patch',
            name: 'apply_patch',
            arguments: { patch: PATCH_TEXT },
          },
        },
      },
    },
  });
  await emit('opencraft:ui', {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_result',
          result: {
            call_id: 'call-patch',
            content: {
              parts: [
                {
                  type: 'text',
                  text: JSON.stringify({
                    files: PATCH_FILES.map((f) => ({
                      path: f.path,
                      action: f.action,
                    })),
                  }),
                },
              ],
            },
            is_error: false,
          },
        },
      },
    },
  });
  await page.waitForTimeout(500);
  await shot(page, '42-patch');
  // The viewport hides the third file; the footer is the way to the
  // rest of the diff and the fade above it is the hint that it exists.
  await page.getByRole('button', { name: 'Show full diff' }).click();
  await page.waitForTimeout(300);
  await shot(page, '42b-patch-expanded');
  // Back to the collapsed viewport before switching themes: the light
  // shots are about the fade and the footer, not another expanded pass.
  await page.getByRole('button', { name: 'Collapse diff' }).click();
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await page.waitForTimeout(200);
  await shot(page, '43-patch-light');
  await page.getByRole('button', { name: 'Show full diff' }).click();
  await page.waitForTimeout(300);
  await shot(page, '43b-patch-light-expanded');
});

// Tool cards carry most of a transcript's scroll, so this surface pins
// the three shapes around them: a command burst that is still running
// (folds to one line with the step in flight, its elapsed time and the
// progress counter on the right), a burst that failed (names the step),
// and a view_image result that renders the frame the model was shown.
test('tool cards', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Fix the flaky suite');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  const delta = (part: unknown) =>
    emit('opencraft:ui', {
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });
  const call = (id: string, name: string, args: unknown) =>
    delta({ type: 'tool_call', call: { id, name, arguments: args } });
  const result = (id: string, parts: unknown[], isError = false) =>
    delta({
      type: 'tool_result',
      result: { call_id: id, content: { parts }, is_error: isError },
    });
  const text = (body: string) => [{ type: 'text', text: body }];

  await call('call-b1', 'exec_command', {
    command: 'npm run build --prefix frontend',
  });
  await result(
    'call-b1',
    text('{"exit_code":0,"stdout":"✓ built in 4.24s","stderr":""}'),
  );
  await call('call-b2', 'exec_command', {
    command: 'npm test --prefix frontend',
  });
  await result(
    'call-b2',
    text('{"exit_code":1,"stdout":"","stderr":"1 failed | 63 passed (64)"}'),
    true,
  );
  // Left in flight on purpose: the burst stays live for the screenshot.
  await call('call-b3', 'exec_command', { command: 'go vet ./...' });

  await delta({ type: 'text', text: 'The suite fails here:' });
  // The bytes the backend ships are a downscaled JPEG data URL; drawn
  // here instead of checked in as a fixture so the audit stays one file.
  const frame = await page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = 480;
    canvas.height = 300;
    const ctx = canvas.getContext('2d') as CanvasRenderingContext2D;
    ctx.fillStyle = '#0f1115';
    ctx.fillRect(0, 0, 480, 300);
    ctx.fillStyle = '#161a22';
    ctx.fillRect(0, 0, 480, 36);
    ctx.fillStyle = '#7c9cff';
    ctx.fillRect(16, 13, 10, 10);
    ctx.fillStyle = '#2a3140';
    ctx.fillRect(0, 36, 160, 264);
    ctx.fillStyle = '#1c212b';
    ctx.fillRect(184, 60, 272, 60);
    ctx.fillRect(184, 136, 272, 60);
    ctx.fillRect(184, 212, 272, 60);
    ctx.fillStyle = '#8b98ad';
    ctx.font = '13px sans-serif';
    ctx.fillText('Commands', 200, 84);
    ctx.fillText('Files', 200, 160);
    ctx.fillText('Usage', 200, 236);
    ctx.fillStyle = '#5b6678';
    ctx.fillRect(200, 92, 180, 6);
    ctx.fillRect(200, 168, 140, 6);
    ctx.fillRect(200, 244, 200, 6);
    return canvas.toDataURL('image/jpeg');
  });
  const base64 = frame.replace(/^data:image\/jpeg;base64,/, '');
  await call('call-v1', 'view_image', { path: 'shots/preview.png' });
  await result('call-v1', [
    {
      type: 'image',
      source: { kind: 'inline', media_type: 'image/jpeg', data: base64 },
    },
    {
      type: 'text',
      text: 'view_image: shots/preview.png (960x600, 48210 bytes)',
    },
  ]);
  await page.getByRole('button', { name: /shots\/preview\.png/ }).click();
  // The running step's clock only shows whole seconds.
  await page.waitForTimeout(1200);
  await shot(page, '44-tool-cards');
});

// A burst that is already finished while the answer is still streaming:
// the header holds the call that just ran — its name and a frozen clock,
// with no progress counter left to show — instead of blanking between
// calls, which is what made a fast burst unreadable.
test('settled tool burst', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Run the suite');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  const delta = (part: unknown) =>
    emit('opencraft:ui', {
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });
  const call = (id: string, name: string, args: unknown) =>
    delta({ type: 'tool_call', call: { id, name, arguments: args } });
  const result = (id: string, stdout: string) =>
    delta({
      type: 'tool_result',
      result: {
        call_id: id,
        content: {
          parts: [
            {
              type: 'text',
              text: `{"exit_code":0,"stdout":"${stdout}","stderr":""}`,
            },
          ],
        },
      },
    });

  await call('call-s1', 'exec_command', { command: 'go build ./...' });
  await result('call-s1', '');
  await call('call-s2', 'exec_command', { command: 'go test ./...' });
  await page.waitForTimeout(1200);
  await result('call-s2', 'ok all packages');
  // The answer keeps streaming, so the burst is live but no longer
  // running: the header still names the call that just finished.
  await delta({ type: 'text', text: 'The suite passes.' });
  await page.waitForTimeout(300);
  await shot(page, '44b-tool-cards-settled');
});

// The user bubble renders its text as Markdown. This surface pins the
// block shapes a bubble has to carry (heading, list, quote, inline code
// chip, fenced block) against the tinted background in both themes.
test('markdown user bubble', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [
      {
        id: 's-md',
        title: 'Markdown bubble',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-02T00:00:00Z',
        messages: 2,
        total_tokens: 0,
      },
    ],
    sessionTurns: [
      {
        seq: 1,
        at: '2026-01-01T00:00:00Z',
        messages: [
          {
            role: 'user',
            content: {
              parts: [
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
            },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'On it.' }] },
          },
        ],
        artifacts: [],
      },
    ],
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Markdown bubble' }).click();
  await page.waitForTimeout(400);
  await shot(page, '45-bubble-markdown');
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await page.waitForTimeout(200);
  await shot(page, '45b-bubble-markdown-light');
});

// The collapsed group header of a finished turn: it keeps saying what
// the burst ran — the count, the last command, and the failure count
// closing the row on the right — instead of collapsing back to a bare
// count once the turn ends. Nothing on this screen is live.
test('collapsed tool group header', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Run the checks');
  await page.getByRole('button', { name: 'Send' }).click();
  const emit = emitter(page);
  const delta = (part: unknown) =>
    emit('opencraft:ui', {
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });
  const call = (id: string, command: string) =>
    delta({
      type: 'tool_call',
      call: { id, name: 'exec_command', arguments: { command } },
    });
  const result = (id: string, text: string, isError = false) =>
    delta({
      type: 'tool_result',
      result: {
        call_id: id,
        content: { parts: [{ type: 'text', text }] },
        is_error: isError,
      },
    });

  await call('call-1', 'go build ./...');
  await result('call-1', '{"exit_code":0,"stdout":"","stderr":""}');
  await call('call-2', 'go test ./...');
  await result('call-2', '{"exit_code":1,"stdout":"","stderr":"FAIL"}', true);
  await call(
    'call-3',
    'go test ./internal/foundation/utils/summarytext/... -run TestCompactFold',
  );
  await result('call-3', '{"exit_code":0,"stdout":"ok","stderr":""}');
  await delta({ type: 'text', text: 'One suite failed; the rest is green.' });
  await emit('opencraft:ui', {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await page.waitForTimeout(300);
  await shot(page, '46-tool-group-header');
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await page.waitForTimeout(200);
  await shot(page, '46b-tool-group-header-light');
});

// A mid-turn interjection is a note in the transcript, not a second
// bubble opening a turn — and the note says which state it is in:
// waiting for a round boundary, taken by one, or left over because the
// turn ended first. Two turns sit on screen so the states can be
// compared in one shot: the settled turn's note under the reply it was
// answered in, the live turn's note still waiting (shot while it
// waits), and then the note that turn walked away from with its
// resend / copy / discard row.
test('steer notes', async ({ page }) => {
  await page.addInitScript(mockBackend as never, { workspace: WS });
  await page.goto('/');
  const emit = emitter(page);
  const delta = (runID: string, part: unknown) =>
    emit('opencraft:ui', {
      type: 'stream',
      data: {
        run_id: runID,
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });
  const end = (runID: string, data: Record<string, unknown> = {}) =>
    emit('opencraft:ui', {
      type: 'turn_end',
      data: {
        run_id: runID,
        conversation_id: 's-1',
        status: 'completed',
        ...data,
      },
    });
  const steer = async (text: string) => {
    await typeComposerMessage(page, text);
    await page.keyboard.press('Enter');
    await page.waitForTimeout(200);
  };

  // Turn one is the happy path: a boundary takes the interjection and
  // the reply carries on under it.
  await typeComposerMessage(page, 'Split the session store by feature');
  await page.getByRole('button', { name: 'Send' }).click();
  await delta('r-1', {
    type: 'text',
    text: 'Reading the store first, then I will split it.\n\n',
  });
  await steer('keep the archive tags working');
  await delta('r-1', {
    type: 'text',
    text: 'Good point — checking that path now.',
  });
  await end('r-1', { steer_pending: 0, duration_ms: 42000 });
  await page.waitForTimeout(250);

  // Turn two is the one that ends before a boundary reads the queue.
  await typeComposerMessage(
    page,
    'Now move the usage recorder out of the desktop',
  );
  await page.getByRole('button', { name: 'Send' }).click();
  await delta('r-2', {
    type: 'text',
    text: 'The recorder hangs off the host now.\n\n',
  });
  await steer('also keep the per-workspace split');
  await page.waitForTimeout(150);
  await shot(page, '47-steer-notes-pending');
  await end('r-2', { steer_pending: 1, duration_ms: 12000 });
  await page.waitForTimeout(300);
  await shot(page, '47b-steer-notes');
  await page.evaluate(() => {
    document.documentElement.classList.add('theme-light');
  });
  await page.waitForTimeout(200);
  await shot(page, '47c-steer-notes-light');
});

// The chat header is the pane's title bar: the conversation's identity on
// the left, its run state next to it, the viewer toggle on the right. It
// is also the one strip that animates outside overlay chrome, so the
// states are shot separately: parked, running, ended on a failure, and
// the light theme (where every tint has to survive the flipped palette).
const HEADER_SESSION = {
  id: 's-1',
  workspace: WS,
  title: 'Rework the usage hero card',
  created_at: '2026-09-14T03:47:36Z',
  updated_at: '2026-09-15T03:47:36Z',
  status: 'idle',
  turns: 4,
  messages: 9,
  total_tokens: 128394,
};

async function openHeaderSession(page: Page) {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    listSessions: [HEADER_SESSION],
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await page.getByRole('button', { name: HEADER_SESSION.title }).click();
  await page.waitForTimeout(400);
}

test.describe('chat header', () => {
  test.use({ deviceScaleFactor: 2 });

  test('parked', async ({ page }) => {
    await openHeaderSession(page);
    await headerShot(page, '70-chat-header');
  });

  test('running', async ({ page }) => {
    await openHeaderSession(page);
    await typeComposerMessage(page, 'Split the hero into two rows');
    await page.getByRole('button', { name: 'Send' }).click();
    const emit = emitter(page);
    // A tool part is what moves the run's stage, and the stage text is
    // the header's live half.
    await emit('opencraft:ui', {
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_call',
            call: {
              id: 'call-1',
              name: 'exec_command',
              arguments: { command: 'npm test --prefix frontend' },
            },
          },
        },
      },
    });
    // Long enough for the call's clock to pass the second that makes the
    // elapsed tick appear, and for the run sweep to sit mid-strip rather
    // than off its left end.
    await page.waitForTimeout(2200);
    await headerShot(page, '71-chat-header-running');
    await page.evaluate(() => {
      document.documentElement.classList.add('theme-light');
    });
    await page.waitForTimeout(300);
    await headerShot(page, '72-chat-header-running-light');
  });

  test('failed', async ({ page }) => {
    await openHeaderSession(page);
    await typeComposerMessage(page, 'Split the hero into two rows');
    await page.getByRole('button', { name: 'Send' }).click();
    const emit = emitter(page);
    await emit('opencraft:ui', {
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'failed',
        error: 'inference: request failed: 502 bad gateway',
      },
    });
    await page.waitForTimeout(400);
    await headerShot(page, '73-chat-header-failed');
  });
});

// The seam between the sidebar and the work column is a 1px hairline the
// resize handle owns, with a knob at its middle. Shot at 2x around the
// knob — the header shots already carry the top of the same line, and
// what needs reviewing here is a hairline between two panel surfaces
// plus the handle's three states: at rest, hovered, and dragging.
const SEAM = 'Resize sidebar';

async function seamBox(page: Page) {
  const box = await page.getByRole('separator', { name: SEAM }).boundingBox();
  if (box === null) throw new Error('the sidebar seam has no box');
  return box;
}

async function columnWidth(page: Page) {
  const box = await page.locator('aside').first().boundingBox();
  if (box === null) throw new Error('the sidebar has no box');
  return Math.round(box.width);
}

function seamClip(box: { x: number; y: number; height: number }) {
  return {
    x: Math.round(box.x - 88),
    y: Math.round(box.y + box.height / 2 - 60),
    width: 176,
    height: 120,
  };
}

test.describe('sidebar seam', () => {
  test.use({ deviceScaleFactor: 2 });

  test('rest, hover and dragging', async ({ page }) => {
    await openHeaderSession(page);
    const box = await seamBox(page);
    const midY = box.y + box.height / 2;
    // The click that opened the session left the pointer on its row, and
    // the row's hover card covers the seam at this height.
    await page.mouse.move(1200, 880);
    await page.waitForTimeout(400);
    await page.screenshot({
      path: `${SHOTS}/74-sidebar-seam.png`,
      clip: seamClip(box),
    });

    // Hovering the knob is the handle's lit state; the shot is taken
    // before the hint's 320ms hover intent, so the seam stays in view.
    await page.mouse.move(box.x + 3, midY);
    await page.waitForTimeout(200);
    await page.screenshot({
      path: `${SHOTS}/75-sidebar-seam-hover.png`,
      clip: seamClip(box),
    });

    // And a live drag: the accent state, with the column narrowed far
    // enough that the pointer sits outside the seam it is dragging.
    const before = await columnWidth(page);
    // The drag is measured from the press point, so the press has to
    // land on the seam itself; the hover above deliberately sits 3px
    // into the knob, and those pixels would be added to the drag.
    await page.mouse.move(box.x + 1, midY);
    await page.mouse.down();
    await page.mouse.move(box.x - 39, midY);
    await page.waitForTimeout(200);
    await page.screenshot({
      path: `${SHOTS}/76-sidebar-seam-dragging.png`,
      clip: seamClip(await seamBox(page)),
    });
    await page.mouse.up();
    // A screenshot suite cannot tell a dead handle from a live one, and
    // the shot above would look the same either way.
    expect(await columnWidth(page)).toBe(before - 40);
  });
});
