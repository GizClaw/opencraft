import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

test('sends a message and renders the streamed tool call', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'hello world');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(
    page.getByTestId('chat-scroll').getByText('hello world'),
  ).toBeVisible();

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
        part: {
          type: 'tool_call',
          call: {
            id: 'call-1',
            name: 'exec_command',
            arguments: { command: 'ls' },
          },
        },
      },
    },
  });
  await expect(page.getByText('$ ls')).toBeVisible();

  await emit({
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
                  text: '{"exit_code":0,"stdout":"README.md\\n","stderr":""}',
                },
              ],
            },
            is_error: false,
          },
        },
      },
    },
  });
  await expect(page.getByText('README.md').first()).toBeVisible();

  await emit({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: 'done' } },
    },
  });
  await expect(page.getByText('done')).toBeVisible();

  await emit({
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
});

test('renders an exec_session read with the output it pulled', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'start the dev server');
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
  const call = (id: string, args: Record<string, unknown>) => ({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: { id, name: 'exec_session', arguments: args },
        },
      },
    },
  });
  const result = (id: string, text: string) => ({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_result',
          result: {
            call_id: id,
            content: { parts: [{ type: 'text', text }] },
            is_error: false,
          },
        },
      },
    },
  });

  await emit(
    call('call-s1', {
      action: 'start',
      process_id: 'dev',
      argv: ['npm', 'run', 'dev'],
    }),
  );
  await emit(result('call-s1', '{"process_id":"dev","started":true}'));
  await emit(
    call('call-s2', { action: 'read', process_id: 'dev', after_seq: 0 }),
  );
  await emit(
    result(
      'call-s2',
      JSON.stringify({
        process_id: 'dev',
        chunks: [
          { seq: 0, stream: 'stdout', data: 'VITE ready in 240 ms\n' },
          { seq: 24, stream: 'stderr', data: 'warning: no lockfile\n' },
        ],
        next_seq: 48,
        eof: false,
      }),
    ),
  );

  // Both calls are command tools, so they fold into one collapsed group.
  await page.getByRole('button', { name: /Ran 2 commands/ }).click();
  await expect(page.getByRole('button', { name: /npm run dev/ })).toBeVisible();

  // The read card names the session, carries its cursor and shows both
  // streams of the pulled output once expanded.
  const read = page.getByRole('button', { name: /session.*dev.*read/ });
  await expect(read).toContainText('#48');
  await read.click();
  await expect(page.getByText('warning: no lockfile')).toBeVisible();
  await expect(page.getByText('VITE ready in 240 ms')).toHaveCount(2);
});

test('renders the image part view_image returned', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'look at the screenshot');
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

  // Stand-in for the downscaled frame the backend hands back. The point
  // is that the bytes travel with the result: the card shows exactly
  // what the model saw, and never needs the file on disk to do it.
  const base64 = '/9j/4AAQSkZJRgABAQAAAQ==';
  await emit({
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: {
          type: 'tool_call',
          call: {
            id: 'call-i1',
            name: 'view_image',
            arguments: { path: 'shots/hero.png' },
          },
        },
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
          type: 'tool_result',
          result: {
            call_id: 'call-i1',
            content: {
              parts: [
                {
                  type: 'image',
                  source: {
                    kind: 'inline',
                    media_type: 'image/jpeg',
                    data: base64,
                  },
                },
                {
                  type: 'text',
                  text: 'view_image: shots/hero.png (1440x900, 123456 bytes)',
                },
              ],
            },
            is_error: false,
          },
        },
      },
    },
  });

  // A lone call renders as its card rather than a one-line group; the
  // header names the file, and expanding renders the frame the model
  // was shown straight from the bytes the result carried.
  const card = page.getByRole('button', { name: /shots\/hero\.png/ });
  await expect(card).toContainText('1440×900');
  await card.click();
  await expect(
    page.getByTestId('chat-scroll').getByRole('img'),
  ).toHaveAttribute('src', `data:image/jpeg;base64,${base64}`);
  await expect(
    page.getByTestId('chat-scroll').getByText('Model saw 1440×900 · 123.5k'),
  ).toBeVisible();
});

// The collapsed group header is the only thing a folded burst says, so
// its layout is the information: the count, the command it stopped on
// (left-aligned right after the count, with the room to show it whole),
// and — closing the row on the right — how many calls failed. The turn
// ends before anything is measured: a settled burst keeps its line.
test('reads the collapsed tool group header left to right', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');

  await typeComposerMessage(page, 'run the checks');
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
  const call = (id: string, command: string) =>
    emit({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_call',
            call: { id, name: 'exec_command', arguments: { command } },
          },
        },
      },
    });
  const result = (id: string, text: string, isError = false) =>
    emit({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_result',
            result: {
              call_id: id,
              content: { parts: [{ type: 'text', text }] },
              is_error: isError,
            },
          },
        },
      },
    });

  await call('call-1', 'go build ./...');
  await result('call-1', '{"exit_code":0,"stdout":"","stderr":""}');
  await call('call-2', 'go test ./...');
  await result('call-2', '{"exit_code":1,"stdout":"","stderr":"FAIL"}', true);
  // Long enough that the old 10rem cap on the step line would have cut
  // it in half.
  await call(
    'call-3',
    'go test ./internal/foundation/utils/summarytext/... -run TestCompactFold -count=1',
  );
  await result('call-3', '{"exit_code":0,"stdout":"ok","stderr":""}');
  await emit({
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });

  const header = page.getByRole('button', { name: /Ran 3 commands/ });
  const step = page.getByTestId('tool-group-step');
  const failed = page.getByTestId('tool-group-failed');
  await expect(step).toBeVisible();
  await expect(step).toContainText('TestCompactFold');
  // The one failure is named as well: it is not the call the burst
  // stopped on, so the row carries both.
  await expect(failed).toContainText('1 step failed');
  await expect(failed).toContainText('go test ./...');

  const headerBox = (await header.boundingBox())!;
  const stepBox = (await step.boundingBox())!;
  const failedBox = (await failed.boundingBox())!;
  const chevronBox = (await header.locator('svg').last().boundingBox())!;

  // The step line begins right after the count: it is not pushed to the
  // far end of the row.
  expect(stepBox.x).toBeGreaterThan(headerBox.x);
  expect(stepBox.x).toBeLessThan(headerBox.x + headerBox.width * 0.45);
  // It owns the free width, so the command renders whole where the cap
  // used to clip it.
  expect(stepBox.width).toBeGreaterThan(headerBox.width * 0.35);
  expect(
    await step.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
  ).toBe(true);
  // The failure count closes the row: right-aligned, one gap shy of the
  // chevron.
  const tail = chevronBox.x - (failedBox.x + failedBox.width);
  expect(tail).toBeGreaterThanOrEqual(0);
  expect(tail).toBeLessThan(16);
});
