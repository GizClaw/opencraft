import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

// The applications page end to end: a package is imported, the install
// lands in the registry the page lists, opening it evaluates the bundle
// the host serves, and the application's own conversation streams a reply
// and the file it wrote.
//
// What this covers that the component tests cannot: the bundle is a real
// ES module evaluated in a real browser (the host's loader, a Blob URL,
// the host's React), and the turn is the one stream the whole window
// shares — the page has to attribute the events it gets to its own
// application and its own conversation, and the window's transcript must
// stay untouched by any of them.

// The bundle the "Hello" package ships: one view, so the tab bar proves
// the module was fetched, evaluated and applied rather than merely read.
const bundle = `
export function apply(ctx) {
  ctx.app.views.add({
    id: 'notes',
    title: 'Notes',
    Component: () => ctx.react.createElement(
      'p',
      { 'data-testid': 'app-view' },
      'the bundle is live',
    ),
  });
}
`;

function emitEvent(page: Page, data: unknown) {
  return page.evaluate(
    (d) =>
      (window as never as { __emit: (n: string, v: unknown) => void }).__emit(
        'opencraft:ui',
        d,
      ),
    data,
  );
}

test('an imported application runs its bundle and talks on its own stream', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [],
    appPackage: { summary: { id: 'hello', name: 'Hello' }, path: '/tmp/hello' },
    appBundle: { entry: 'ui/dist/index.js', source: bundle },
    appTurn: { run_id: 'r-app-1', conversation_id: 's-app-1' },
  });
  await page.goto('/');

  // The page is one of the tools pages, so it opens the way the others
  // do — from the sidebar, not from a route.
  await page.getByRole('button', { name: 'Applications' }).click();
  // Two empty states say the same thing — the sidebar column and the
  // gallery — so the one under test is the gallery's.
  await expect(page.getByText('No applications yet').last()).toBeVisible();

  // The wizard: pick a directory, read what installing it would do, then
  // install it. The pick is a read — nothing is copied until the confirm.
  await page.getByRole('button', { name: 'Import application' }).click();
  await page.getByTestId('app-pick-folder').click();
  // What the wizard read: the identity the install would land under.
  await expect(page.getByText('hello · 1.0.0 · hello')).toBeVisible();
  await page.getByTestId('app-install-confirm').click();

  // The install enables the application and opens its page: the built-in
  // conversation is there, with the bundle's view beside it.
  await expect(page.getByTestId('app-tab-notes')).toBeVisible();
  await page.getByRole('tab', { name: 'Notes' }).click();
  await expect(page.getByTestId('app-view')).toHaveText('the bundle is live');

  // The conversation tab is the floor of the page: it is always there,
  // whether or not the bundle registered views, and a page with views
  // opens on one of *them* — so the composer is one click away.
  await page.getByRole('tab', { name: 'Chat' }).click();
  const composer = page.getByTestId('app-composer');
  await expect(composer).toBeVisible();

  await composer.fill('write me a file');
  await composer.press('Enter');
  await expect(page.getByTestId('app-msg-user')).toHaveText('write me a file');

  // The turn's events, named with the application and the conversation
  // the start call answered with. The first delta clears the spinner; the
  // artifact lands in the strip; the end stops the run.
  const stream = (text: string) =>
    emitEvent(page, {
      type: 'stream',
      data: {
        app_id: 'hello',
        run_id: 'r-app-1',
        conversation_id: 's-app-1',
        delta: { type: 'part', part: { type: 'text', text } },
      },
    });
  await stream('writing ');
  await expect(page.getByTestId('app-msg-assistant')).toContainText('writing ');
  await stream('hello.txt');
  await emitEvent(page, {
    type: 'artifact',
    data: {
      app_id: 'hello',
      conversation_id: 's-app-1',
      run_id: 'r-app-1',
      path: 'hello.txt',
      bytes: 20,
    },
  });
  await emitEvent(page, {
    type: 'turn_end',
    data: {
      app_id: 'hello',
      conversation_id: 's-app-1',
      run_id: 'r-app-1',
      status: 'completed',
    },
  });

  await expect(page.getByTestId('app-msg-assistant')).toHaveText(
    'writing hello.txt',
  );
  await expect(page.getByTestId('app-artifact')).toHaveText('hello.txt');
  // The turn is over, so the composer is a composer again rather than a
  // stop button.
  await expect(page.getByRole('button', { name: 'Send' })).toBeVisible();

  // And none of it reached the assistant. Closing the page is what
  // brings the window's own surface back — the application's page is a
  // tool page, not a route — and the transcript it returns to has none
  // of the application's turn in it, because that conversation belongs
  // to the application.
  await page.getByRole('button', { name: 'Applications' }).click();
  await expect(page.getByTestId('chat-scroll')).not.toContainText(
    'writing hello.txt',
  );
});

test("another conversation's events are not the page's", async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [],
    appPackage: { summary: { id: 'hello', name: 'Hello' }, path: '/tmp/hello' },
    appTurn: { run_id: 'r-app-1', conversation_id: 's-app-1' },
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Applications' }).click();
  await page.getByRole('button', { name: 'Import application' }).click();
  await page.getByTestId('app-pick-folder').click();
  await page.getByTestId('app-install-confirm').click();

  const composer = page.getByTestId('app-composer');
  await expect(composer).toBeVisible();
  await composer.fill('first');
  await composer.press('Enter');
  await expect(page.getByTestId('app-msg-user')).toHaveText('first');

  // A run the page did not start — the application's own script, another
  // page of the same application — naming another conversation. Its
  // deltas and its file are not this transcript's, and its end must not
  // clear the spinner of the turn that is still running.
  await emitEvent(page, {
    type: 'stream',
    data: {
      app_id: 'hello',
      run_id: 'r-other',
      conversation_id: 's-other',
      delta: { type: 'part', part: { type: 'text', text: 'not mine' } },
    },
  });
  await emitEvent(page, {
    type: 'artifact',
    data: {
      app_id: 'hello',
      conversation_id: 's-other',
      run_id: 'r-other',
      path: 'elsewhere.txt',
      bytes: 5,
    },
  });
  await emitEvent(page, {
    type: 'turn_end',
    data: {
      app_id: 'hello',
      conversation_id: 's-other',
      run_id: 'r-other',
      status: 'completed',
    },
  });

  await expect(page.getByTestId('app-msg-assistant')).toHaveCount(0);
  await expect(page.getByTestId('app-artifacts')).toHaveCount(0);
  // The page's own turn is still running: its stop button is up.
  await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();

  // Its own events, by contrast, land: an end that names the run the
  // page is watching stops it.
  await emitEvent(page, {
    type: 'stream',
    data: {
      app_id: 'hello',
      run_id: 'r-app-1',
      conversation_id: 's-app-1',
      delta: { type: 'part', part: { type: 'text', text: 'mine' } },
    },
  });
  await emitEvent(page, {
    type: 'turn_end',
    data: {
      app_id: 'hello',
      conversation_id: 's-app-1',
      run_id: 'r-app-1',
      status: 'completed',
    },
  });
  await expect(page.getByTestId('app-msg-assistant')).toHaveText('mine');
  await expect(page.getByRole('button', { name: 'Send' })).toBeVisible();
});

test('an application that is switched off closes its page', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [{ id: 'hello', name: 'Hello', version: '1.0.0', enabled: true }],
    appBundle: { entry: 'ui/dist/index.js', source: bundle },
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Applications' }).click();

  await page.getByTestId('app-nav-hello').click();
  // The page opens on the bundle's view; the switch it has to survive is
  // about the whole application, not the tab it happens to be showing.
  await page.getByRole('tab', { name: 'Chat' }).click();
  await expect(page.getByTestId('app-composer')).toBeVisible();

  // The registry change is not a page action: the host announces it (a
  // disable from another window, the agent, a failed update), and the
  // page has to decide from the resulting list rather than the event.
  // A page whose turns would be refused and whose roots are being torn
  // down under it closes; the card is what is left.
  await page.evaluate(
    (args) =>
      (
        window as never as {
          __ocMockByModule: Record<
            string,
            Record<string, (...a: unknown[]) => unknown>
          >;
        }
      ).__ocMockByModule.App.SetEnabled(...args),
    ['hello', false],
  );
  await expect(page.getByTestId('app-composer')).toHaveCount(0);
  // The card is what is left, marked with the state the registry now
  // holds (the sidebar row carries the same badge, so this reads the
  // card's own).
  const card = page.getByTestId('app-card-hello');
  await expect(card).toBeVisible();
  await expect(card.getByText('Disabled')).toBeVisible();
});

test('the wizard reports a package the checks refuse', async ({ page }) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [],
    appPackage: {
      summary: { id: 'hello', name: 'Hello' },
      path: '/tmp/hello',
      refusals: [
        {
          Layer: 'layer.yaml',
          Key: 'infer',
          Reason: 'a reserved key',
        },
        {
          Layer: 'layer.yaml',
          Key: 'agents.main',
          Reason: '{file: prompt.md} leaves the content root',
        },
      ],
    },
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Applications' }).click();
  await page.getByRole('button', { name: 'Import application' }).click();
  await page.getByTestId('app-pick-folder').click();

  // One row per problem with the key that carries it, and no install: the
  // verdict is the preflight's and the confirm button is what has to say
  // so.
  await expect(page.getByText('a reserved key')).toBeVisible();
  await expect(page.getByText('leaves the content root')).toBeVisible();
  await expect(page.getByTestId('app-install-confirm')).toBeDisabled();
});

test('a bundle that fails to apply leaves the conversation usable', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [{ id: 'hello', name: 'Hello', version: '1.0.0', enabled: true }],
    appBundle: {
      entry: 'ui/dist/index.js',
      source: 'export function apply() { throw new Error("boom") }\n',
    },
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Applications' }).click();
  await page.getByTestId('app-nav-hello').click();

  // A frontend failure is the author's problem, not the page's: the error
  // is printed beside a conversation that still works.
  await expect(page.getByTestId('app-view-error')).toContainText('boom');
  const composer = page.getByTestId('app-composer');
  await expect(composer).toBeVisible();
  await composer.fill('still here');
  await composer.press('Enter');
  await expect(page.getByTestId('app-msg-user')).toHaveText('still here');
});

test('the diagnostics panel shows the three roots and the workspace', async ({
  page,
}) => {
  await page.addInitScript(mockBackend as never, {
    workspace: '/workspace',
    apps: [{ id: 'hello', name: 'Hello', version: '1.0.0', enabled: true }],
    handlers: handlerSources({
      'App.ListFiles': async () => [
        { name: 'hello.txt', path: 'hello.txt', is_dir: false, size: 20 },
      ],
      'App.ReadFile': async () => 'written by the app\n',
    }),
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Applications' }).click();
  await page.getByTestId('app-nav-hello').click();
  await page.getByTestId('app-diagnostics-toggle').click();

  // The page a broken application sends you to: where its files are, and
  // what a script wrote — read out of the private workspace, not guessed
  // from the content root.
  await expect(
    page.getByText('/apps/hello/content', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText('/apps/hello', { exact: true })).toBeVisible();
  await expect(
    page.getByText('/apps/hello/workspace', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'hello.txt' }).click();
  await expect(page.getByText('written by the app')).toBeVisible();
});
