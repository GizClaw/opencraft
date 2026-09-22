import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

const WS = '/Users/me/projects/opencraft';

// emit pushes one UI event through the mock's dispatch hook, the same
// way the Go side streams a running turn.
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

async function startChat(page: Page) {
  await page.addInitScript(mockBackend as never, {
    workspace: WS,
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'Add the usage hero card');
  await page.getByRole('button', { name: 'Send' }).click();
  await emit(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: {
        type: 'part',
        part: { type: 'text', text: 'Reworking the hero card.\n' },
      },
    },
  });
}

async function emitPlan(page: Page) {
  await emit(page, {
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
}

const boxOf = async (locator: ReturnType<Page['getByTestId']>) => {
  const box = await locator.boundingBox();
  if (!box) throw new Error('element has no box');
  return {
    x: Math.round(box.x),
    y: Math.round(box.y),
    width: Math.round(box.width),
    height: Math.round(box.height),
  };
};

test('floats the current plan in the top-left corner without reserving a row', async ({
  page,
}) => {
  await startChat(page);
  const scroll = page.getByTestId('chat-scroll');
  await expect(scroll.getByText('Reworking the hero card.')).toBeVisible();
  const before = await boxOf(scroll);

  await emitPlan(page);
  const card = page.getByTestId('activity-card');
  await expect(card.getByText('Wire the range picker')).toBeVisible();

  // The transcript keeps the whole column: the card is an overlay, so
  // the conversation never loses a row to a running plan.
  expect(await boxOf(scroll)).toEqual(before);
  // Pinned to the top-left corner: the card hugs the column's left and
  // top edges, and its own width is a fraction of the column instead of
  // the full-width band a docked card would take.
  const box = await boxOf(card);
  expect(box.x - before.x).toBeLessThan(24);
  expect(box.y - before.y).toBeLessThan(24);
  expect(box.width).toBeLessThan(before.width / 2);
});

test('keeps the plan card put while the file panel opens and closes', async ({
  page,
}) => {
  await startChat(page);
  await emitPlan(page);
  const card = page.getByTestId('activity-card');
  const scroll = page.getByTestId('chat-scroll');
  await expect(card.getByText('Run the e2e suite')).toBeVisible();
  const pinned = await boxOf(card);
  const wide = await boxOf(scroll);

  const toggle = page.getByRole('button', { name: 'Show / hide file viewer' });
  await toggle.click();
  // The panel takes its width out of the chat column, which is what
  // resized the card back when it sat in the flow.
  await expect(async () => {
    expect((await boxOf(scroll)).width).toBeLessThan(wide.width);
  }).toPass();
  // The card does not reflow with the column: same corner, same rows.
  // Its width gives way only to the clamp that keeps it inside a column
  // too narrow to hold it — a few pixels, not a re-layout.
  const narrow = await boxOf(card);
  expect(narrow.x).toBe(pinned.x);
  expect(narrow.y).toBe(pinned.y);
  expect(narrow.height).toBe(pinned.height);
  expect(pinned.width - narrow.width).toBeLessThan(8);
  // Narrow column or not, the card stays inside it.
  expect(narrow.width).toBeLessThan((await boxOf(scroll)).width);

  await toggle.click();
  await expect(async () => {
    expect((await boxOf(scroll)).width).toBe(wide.width);
  }).toPass();
  expect(await boxOf(card)).toEqual(pinned);
});
