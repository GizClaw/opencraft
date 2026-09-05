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
            content: '{"exit_code":0,"stdout":"README.md\\n","stderr":""}',
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
