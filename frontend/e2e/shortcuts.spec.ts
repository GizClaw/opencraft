// The keyboard layer end to end: the shell's dispatcher, the shortcut sheet
// it opens, the actions that reach into the chat surface, and the bridge the
// native macOS menu uses to run the very same commands.
import { expect, test, type Page } from '@playwright/test';
import { mockBackend } from './mock/backend';
import { typeComposerMessage } from './helpers';

async function emitUI(page: Page, data: unknown) {
  await page.evaluate(
    (payload) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        payload,
      ),
    data,
  );
}

/** emitMenu plays the event the native menu bar's items send. */
async function emitMenu(page: Page, command: unknown) {
  await page.evaluate(
    (payload) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:menu',
        payload,
      ),
    { command },
  );
}

test('⌘/ opens the shortcut sheet and Escape gives the composer back', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  const composer = page.locator('.ProseMirror').first();
  await composer.waitFor();
  await composer.click();
  await expect(composer).toBeFocused();

  await page.keyboard.press('ControlOrMeta+/');
  const sheet = page.getByTestId('shortcut-sheet');
  await expect(sheet).toBeVisible();
  // The sheet is rendered from the table the dispatcher reads, so the keys
  // in it are the keys that work.
  await expect(sheet).toContainText('Command palette');

  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);
  await expect(composer).toBeFocused();
});

test('the native menu bridge runs the same commands the keys do', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  await page.locator('.ProseMirror').first().waitFor();

  // A menu item sends its command id and nothing else.
  await emitMenu(page, 'shortcuts.open');
  await expect(page.getByTestId('shortcut-sheet')).toBeVisible();

  // The next command closes the surface it was asked to leave behind.
  await emitMenu(page, 'palette.open');
  await expect(page.getByRole('combobox')).toBeVisible();
  await expect(page.getByTestId('shortcut-sheet')).toHaveCount(0);

  // An id the shell does not know is ignored rather than fatal.
  await emitMenu(page, 'nope.nothing');
  await expect(page.getByRole('combobox')).toBeVisible();
  await emitMenu(page, '');
  await expect(page.getByRole('combobox')).toBeVisible();
});

test('⌘L puts the caret back in the composer', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {} as never);
  await page.goto('/');
  const composer = page.locator('.ProseMirror').first();
  await composer.waitFor();
  await composer.click();
  // Focus leaves the composer (a click on the transcript does it) and the
  // key has to find it again.
  await page.evaluate(() => (document.activeElement as HTMLElement)?.blur());
  await expect(composer).not.toBeFocused();

  await page.keyboard.press('ControlOrMeta+l');
  await expect(composer).toBeFocused();
});

test('Esc stops the running reply', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await recordCancelCalls(page);
  await page.goto('/');
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emitUI(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: 'working' } },
    },
  });
  await expect(
    page.getByRole('button', { name: 'Stop' }).first(),
  ).toBeVisible();

  // Escape is the sheet's key too, but only while a layer is open: with the
  // transcript in front of the user it belongs to the running turn.
  await page.keyboard.press('Escape');

  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as never as { __ocCancelCalls: string[] }).__ocCancelCalls,
      ),
    )
    .toEqual(['r-1']);

  // And the turn is over as far as the transcript is concerned.
  await emitUI(page, {
    type: 'turn_end',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      status: 'interrupted',
      interrupt_cause: 'user_cancel',
    },
  });
  // The header carries the turn's end state for the session, not just the
  // notice inside the scroll: stopped is a property of the transcript now.
  await expect(page.getByTestId('chat-turn-stop')).toContainText(
    'Reply interrupted',
  );
});

test('Esc stops the running reply from inside the composer', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await recordCancelCalls(page);
  await page.goto('/');
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emitUI(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: 'working' } },
    },
  });
  await expect(
    page.getByRole('button', { name: 'Stop' }).first(),
  ).toBeVisible();

  // The composer keeps the caret (it is where the user was typing), and
  // Escape there is the composer's own key — the shell leaves Escape to
  // text surfaces — so this is the path that has to stop the turn.
  const composer = page.locator('.ProseMirror').first();
  await composer.click();
  await expect(composer).toBeFocused();
  await page.keyboard.press('Escape');

  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as never as { __ocCancelCalls: string[] }).__ocCancelCalls,
      ),
    )
    .toEqual(['r-1']);
});

/**
 * recordCancelCalls patches the mock's Conversation module so the stop is
 * observable: the RPC is only real if the run id travelled with it.
 */
async function recordCancelCalls(page: Page) {
  await page.addInitScript(() => {
    const calls: string[] = [];
    (window as never as { __ocCancelCalls: string[] }).__ocCancelCalls = calls;
    const w = window as never as {
      __ocMockByModule?: Record<string, Record<string, unknown>>;
    };
    const patch = () => {
      const conversation = w.__ocMockByModule?.Conversation;
      if (!conversation) return false;
      conversation.CancelTurn = async (runID: string) => {
        calls.push(runID);
      };
      return true;
    };
    // The mock installs its modules with the app; wait for them.
    const timer = setInterval(() => {
      if (patch()) clearInterval(timer);
    }, 10);
  });
}

test('⌘⇧C copies the last reply', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  // The clipboard write is the app's; the browser's copy of it is stubbed so
  // the assertion is about what the shortcut handed over.
  await page.addInitScript(() => {
    const written: string[] = [];
    (window as never as { __ocClipboard: string[] }).__ocClipboard = written;
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (text: string) => {
          written.push(text);
        },
      },
    });
  });
  await page.goto('/');
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  await emitUI(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: 'the answer' } },
    },
  });
  await emitUI(page, {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });

  await page.keyboard.press('ControlOrMeta+Shift+c');
  await expect
    .poll(() =>
      page.evaluate(
        () => (window as never as { __ocClipboard: string[] }).__ocClipboard,
      ),
    )
    .toEqual(['the answer']);
  await expect(page.getByText('Copied the last reply')).toBeVisible();
});

test('⌘↑ walks the transcript question by question', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    currentSession: 's-1',
    startTurn: { run_id: 'r-1', context_id: 's-1' },
  });
  await page.goto('/');
  await typeComposerMessage(page, 'first prompt');
  await page.getByRole('button', { name: 'Send' }).click();
  // A long first answer so the transcript is taller than the viewport: a
  // jump can only park a row at the top when there is enough scroll range
  // below it.
  const filler = Array.from({ length: 80 }, (_, i) => `line ${i}`).join('\n\n');
  await emitUI(page, {
    type: 'stream',
    data: {
      run_id: 'r-1',
      conversation_id: 's-1',
      delta: { type: 'part', part: { type: 'text', text: filler } },
    },
  });
  await emitUI(page, {
    type: 'turn_end',
    data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
  });
  await typeComposerMessage(page, 'second prompt');
  await page.getByRole('button', { name: 'Send' }).click();

  const scroller = page.getByTestId('chat-scroll');
  const second = page
    .locator('[data-msg-index]')
    .filter({ hasText: 'second prompt' });
  await expect(second).toBeVisible();

  // The walk is for a reader, not a typist: ⌘↑ is a text field's own key
  // (jump to the start of the field), so the transcript stands down while
  // the caret is in the composer — the reader's state is focus elsewhere.
  await page.evaluate(() => (document.activeElement as HTMLElement)?.blur());

  // ↑ goes back to the request above the one on screen, not to the previous
  // row: the walk is about turns, which is what a reader is looking for. A
  // jump parks the row 12px below the top edge (jumpToMessage), or as close
  // to it as the content below allows.
  await page.keyboard.press('ControlOrMeta+ArrowUp');
  await expect.poll(() => parked(page, 'first prompt')).toBeLessThan(5);

  await page.keyboard.press('ControlOrMeta+ArrowDown');
  // Down goes to the next request — the newest one, with the running turn's
  // placeholder under it, so again as close to the top as it can get.
  await expect.poll(() => parked(page, 'second prompt')).toBeLessThan(5);
});

// The slot numbers are hints, not labels: they appear while the modifier
// that runs them is held and are out of sight the rest of the time
// (lib/modifierHeld.ts). This is the browser's verdict on it — a class
// name is not a visibility change.
test('the slot numbers appear only while the modifier is held', async ({
  page,
}) => {
  await page.addInitScript(
    mockBackend as never,
    {
      listSessions: [
        {
          id: 's-1',
          title: 'First session',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
          messages: 1,
          total_tokens: 0,
        },
        {
          id: 's-2',
          title: 'Second session',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
          messages: 1,
          total_tokens: 0,
        },
      ],
    } as never,
  );
  await page.goto('/');

  const badges = page.getByTestId('session-slot');
  // The rows keep the badges' place while they are away — hidden, not
  // unmounted — so showing the numbers cannot re-truncate a title.
  await expect(badges).toHaveCount(2);
  await expect(badges.nth(0)).toBeHidden();
  await expect(badges.nth(1)).toBeHidden();

  // Which modifier counts is the app's own platform answer (App's isMac),
  // so ask the page instead of the test runner.
  const mac = await page.evaluate(() =>
    /Macintosh|Mac OS X/i.test(navigator.userAgent),
  );
  const mod = mac
    ? { key: 'Meta', combo: '⌘1', second: '⌘2' }
    : { key: 'Control', combo: 'Ctrl+1', second: 'Ctrl+2' };

  await page.keyboard.down(mod.key);
  await expect(badges.nth(0)).toBeVisible();
  await expect(badges.nth(1)).toBeVisible();
  await expect(badges.nth(0)).toHaveText(mod.combo);

  // The row's own buttons live on the same edge, so the row under the
  // pointer shows them instead of its number.
  await page.getByRole('button', { name: 'Second session' }).hover();
  await expect(badges.nth(1)).toBeHidden();

  await page.keyboard.up(mod.key);
  await expect(badges.nth(0)).toBeHidden();

  // The pointer is still on the second row, and the card it opened carries
  // the number as well — the badge is a hint, and a hint nobody is told
  // about is a hint nobody uses. The card's number does not wait for the
  // modifier: it is how the mouse learns the row's key in the first place.
  await expect(page.getByTestId('session-hover-card')).toContainText(
    mod.second,
  );
});

/**
 * parked reports the distance between the transcript's scroll offset and the
 * offset that would put `text`'s row 12px below the top edge — clamped to the
 * scroll range, so a row near the end of the transcript is "parked" once the
 * view has gone as far as it can.
 */
async function parked(page: Page, text: string): Promise<number> {
  return page.evaluate((needle) => {
    const scroller = document.querySelector<HTMLElement>(
      '[data-testid="chat-scroll"]',
    );
    if (scroller === null) return Number.NaN;
    const rows = Array.from(
      scroller.querySelectorAll<HTMLElement>('[data-msg-index]'),
    );
    const row = rows.find((candidate) =>
      candidate.textContent?.includes(needle),
    );
    if (row === undefined) return Number.NaN;
    const view = scroller.getBoundingClientRect();
    const offset =
      row.getBoundingClientRect().top - view.top + scroller.scrollTop;
    const max = scroller.scrollHeight - scroller.clientHeight;
    const expected = Math.max(0, Math.min(offset - 12, max));
    return Math.abs(scroller.scrollTop - expected);
  }, text);
}
