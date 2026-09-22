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

// A turn's answer arrives in blocks: text, a tool call, more text. Only
// the block the model is still writing is unfinished text; the ones a
// tool call ended are settled, and their markdown is what the reader
// should see while the rest of the turn runs. The transcript used to hold
// every block of a running turn as raw text — `## Plan` sat on screen
// with its hashes for the rest of the turn, then re-laid out at the end.
test('parses a settled block while the turn is still running', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'make a plan');
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
  const delta = (part: unknown) =>
    emit({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });

  await delta({ type: 'text', text: '## Plan\n\n- first step' });
  // The block is still the one being written: hashes stay hashes.
  await expect(page.getByText('## Plan')).toBeVisible();

  await delta({
    type: 'tool_call',
    call: { id: 'call-1', name: 'exec_command', arguments: { command: 'ls' } },
  });
  // The call landed: the card is what the next delta settles.
  await expect(page.getByText('$ ls')).toBeVisible();
  await delta({
    type: 'tool_result',
    result: {
      call_id: 'call-1',
      content: {
        parts: [
          { type: 'text', text: '{"exit_code":0,"stdout":"","stderr":""}' },
        ],
      },
      is_error: false,
    },
  });
  await delta({ type: 'text', text: '## Still writing' });

  // The call ended the first block, so it reads as markdown now; the
  // trailing block is the live one and stays raw until the turn ends.
  await expect(page.getByRole('heading', { name: 'Plan' })).toBeVisible();
  await expect(page.getByText('## Still writing')).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Still writing' }),
  ).toHaveCount(0);

  await emit({
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await expect(
    page.getByRole('heading', { name: 'Still writing' }),
  ).toBeVisible();
});

// The ask card is one row: icon, question, answer chip, chevron. The chip
// grew with the answer, and a multi-choice answer joined its option texts,
// so three long options made a chip wider than the row: the question was
// squeezed to zero width and both painted past the card edge, with no
// ellipsis anywhere because nothing was capped. Measured here — jsdom has
// no layout to catch this in.
test('keeps a long ask question and answer inside the card header', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(mockBackend as never, {
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'go on');
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
  const delta = (part: unknown) =>
    emit({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part },
      },
    });

  const options = [
    'Merge #197 (the flake it fixes is the one that turns main red)',
    'Give TestSessionSignalInterrupts a timeout self-diagnosis (dump the child /proc/<pid>/status, SigBlk + ps process group, so the next red CI carries its own evidence)',
    'Track the execd flake down: rebuild the Linux environment with bwrap + sudo first',
    'Add fuzz failure notifications to the release workflow',
    'Leave all of it for now, I will look at #197 first',
  ];
  await delta({
    type: 'tool_call',
    call: {
      id: 'call-ask',
      name: 'ask_user',
      arguments: {
        question:
          'What next? (#197 all green pending merge, the execd flake I narrowed down)',
        kind: 'select',
        multiple: true,
        options,
      },
    },
  });
  const header = page.getByRole('button', { name: /What next\?/ });
  await expect(header).toBeVisible();
  await delta({
    type: 'tool_result',
    result: {
      call_id: 'call-ask',
      content: {
        parts: [
          {
            type: 'text',
            text: JSON.stringify({
              cancelled: false,
              choice: '',
              choices: options.slice(0, 3),
              other: '',
              text: '',
            }),
          },
        ],
      },
      is_error: false,
    },
  });

  // The chip is the card's settled state; waiting for it keeps the
  // measurements below off the stream flush.
  const chip = header.locator('span').nth(1);
  await expect(chip).toBeVisible();
  const row = (await header.boundingBox())!;
  const question = (await header.locator('span').first().boundingBox())!;

  // The row stays inside the card, and the question keeps its share of it
  // instead of measuring zero — the chip used to be wider than the card.
  expect(
    await header.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
  ).toBe(true);
  expect(question.width).toBeGreaterThan(row.width * 0.4);

  // The answer is named by its count — the joined option texts are what
  // made the chip unbounded — and the chip is capped to its own share.
  await expect(chip).toHaveText('✓ 3 choices');
  const chipBox = (await chip.boundingBox())!;
  expect(chipBox.width).toBeLessThan(row.width * 0.5);
  expect(chipBox.x + chipBox.width).toBeLessThanOrEqual(row.x + row.width + 1);

  // What the chip had to shorten is one hover away.
  await chip.hover();
  await expect(page.getByTestId('tooltip')).toContainText(options[2]);
});
