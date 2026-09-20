// UI audit harness: renders the real frontend against the mock backend
// and dumps one screenshot per surface (dark and light) so a design
// change can be reviewed as before/after images instead of a diff of
// class strings.
//
// Run with `npm run audit:ui --prefix frontend`; it is a separate
// Playwright config (playwright.visual.config.ts), so the CI e2e job
// (testDir: e2e) never picks it up. Screenshots land in
// e2e-visual/shots/ and are gitignored.
import { test, type Page } from '@playwright/test';
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
  await page.waitForTimeout(400);
  await shot(page, '40-chat');
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
