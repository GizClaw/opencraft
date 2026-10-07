import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  appScope,
  disposeAppScope,
  dispatchAppEvent,
  ensureAppScope,
  subscribeAppEvents,
} from './host';
import { UIEventType } from '../lib/events';
import type { AppContext } from './types';

// One bundle, in place of the module the host would evaluate: what the
// tests need is the shape of the interaction (views registered, effects
// asked for, a stylesheet read back), not a real ES module behind a Blob
// URL. `exportApply` is the bundle that implements no protocol.
const bundle = vi.hoisted(() => ({
  apply: vi.fn(),
  exportApply: true,
  style: 'body { --app-style: 1 }',
  views: 0,
  effects: [] as string[],
}));

const apiMock = vi.hoisted(() => ({
  appManifest: vi.fn(),
  appAsset: vi.fn(),
  appStartTurn: vi.fn(),
  version: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../plugins/module', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../plugins/module')>()),
  loadModule: vi.fn(async () =>
    bundle.exportApply ? { apply: bundle.apply } : {},
  ),
}));

/** manifest is one application's app.yaml, as the page reads it. */
function manifest(entry = 'ui/index.js', style = 'ui/index.css') {
  return { app: 'app: v1', id: 'hello', name: 'Hello', ui: { entry, style } };
}

/** asset answers one read out of the content root, by extension. */
function asset(_id: string, rel: string) {
  return Promise.resolve({
    data: rel.endsWith('.css')
      ? btoa(bundle.style)
      : btoa('export function apply() {}'),
    media_type: rel.endsWith('.css') ? 'text/css' : 'text/javascript',
  });
}

/** applyAsBundle installs the fake bundle's apply body. */
function applyAsBundle(awaitAfterRegister = false) {
  bundle.apply.mockImplementation(async (ctx: AppContext) => {
    ctx.app.views.add({
      id: `view-${++bundle.views}`,
      title: `View ${bundle.views}`,
      Component: () => null,
    });
    // Two effects, so their order is observable: the host runs them in
    // reverse, the way nested setup unwinds.
    for (const name of ['first', 'second']) {
      ctx.effect(() => {
        bundle.effects.push(name);
      });
    }
    if (awaitAfterRegister) await gate();
  });
}

// gate is a promise the test releases, so a load can be held open at one
// point while the page walks away.
let releaseGate: () => void = () => {};
let gatePassed = false;
function gate(): Promise<void> {
  if (gatePassed) return Promise.resolve();
  return new Promise<void>((resolve) => {
    releaseGate = () => {
      gatePassed = true;
      resolve();
    };
  });
}

/** stylesheets returns the nodes the host injected for the test app. */
function stylesheets(): HTMLStyleElement[] {
  return [
    ...document.querySelectorAll<HTMLStyleElement>('style[data-app="hello"]'),
  ];
}

beforeEach(() => {
  vi.clearAllMocks();
  // A scope left behind by the previous test would answer this one's
  // load, and its teardown would land in this test's effect log — so it
  // goes first, before the log is cleared.
  disposeAppScope('hello');
  document.head
    .querySelectorAll('style[data-app]')
    .forEach((el) => el.remove());
  bundle.exportApply = true;
  bundle.views = 0;
  bundle.effects = [];
  gatePassed = false;
  applyAsBundle();
  apiMock.appManifest.mockResolvedValue(manifest());
  apiMock.appAsset.mockImplementation(asset);
  apiMock.version.mockResolvedValue('0.0.0-test');
});

describe('app scope lifetime', () => {
  it('applies the bundle once and reads back what it registered', async () => {
    const first = await ensureAppScope('hello');
    const second = await ensureAppScope('hello');

    // StrictMode double-invokes effects, and the page's mount and a tab
    // switch can both ask: a second evaluation would register every view
    // twice.
    expect(bundle.apply).toHaveBeenCalledTimes(1);
    expect(first.views.map((v) => v.id)).toEqual(['view-1']);
    expect(second.views.map((v) => v.id)).toEqual(['view-1']);
    // The stylesheet comes with the scope, as one node the disposal can
    // take back out of the document.
    expect(stylesheets()).toHaveLength(1);
  });

  it('reports a bundle that ships no frontend as a scope without views', async () => {
    apiMock.appManifest.mockResolvedValue(manifest(''));

    const scope = await ensureAppScope('hello');

    // An application that is only a conversation is not a failure: the
    // page shows the conversation tab, and nothing is reported as broken
    // that a user could fix.
    expect(scope.views).toEqual([]);
    expect(scope.error).toBeUndefined();
    expect(bundle.apply).not.toHaveBeenCalled();
  });

  it('reports an apply that throws instead of rejecting the load', async () => {
    bundle.apply.mockRejectedValue(new Error('apply exploded'));

    const scope = await ensureAppScope('hello');

    expect(scope.error).toContain('apply exploded');
    expect(scope.views).toEqual([]);
  });

  it('refuses a bundle that implements no protocol', async () => {
    bundle.exportApply = false;

    const scope = await ensureAppScope('hello');

    expect(scope.error).toContain('apply');
  });

  it('unloads the views, the effects and the stylesheet together', async () => {
    const scope = await ensureAppScope('hello');
    expect(scope.views).toHaveLength(1);

    disposeAppScope('hello');

    // Every piece of a scope goes with it: a view left behind would be
    // rendered by a page whose application is gone, and a stylesheet left
    // behind styles the *next* application's page.
    expect(appScope('hello')).toBeUndefined();
    expect(bundle.effects).toEqual(['second', 'first']);
    expect(stylesheets()).toHaveLength(0);
  });

  it('loads a fresh bundle after a disposal', async () => {
    await ensureAppScope('hello');
    disposeAppScope('hello');

    const again = await ensureAppScope('hello');

    // A reloaded application runs its bundle again — that is what makes
    // an update visible without restarting the window.
    expect(bundle.apply).toHaveBeenCalledTimes(2);
    expect(again.views.map((v) => v.id)).toEqual(['view-2']);
    expect(stylesheets()).toHaveLength(1);
  });

  it('does not apply a bundle whose scope was disposed while it loaded', async () => {
    apiMock.appAsset.mockImplementation(async (_id, rel) => {
      await gate();
      return asset(_id as string, rel as string);
    });

    const loading = ensureAppScope('hello');
    // The page left while the bundle was still being read.
    await vi.waitFor(() => expect(appScope('hello')).toBeDefined());
    disposeAppScope('hello');
    releaseGate();
    const scope = await loading;

    // The load ends without applying: no views, no effects and no
    // stylesheet for a scope nobody holds.
    expect(bundle.apply).not.toHaveBeenCalled();
    expect(scope.views).toEqual([]);
    expect(bundle.effects).toEqual([]);
    expect(stylesheets()).toHaveLength(0);
    expect(appScope('hello')).toBeUndefined();
  });

  it('does not inject the stylesheet of a scope disposed during apply', async () => {
    applyAsBundle(true);

    const loading = ensureAppScope('hello');
    await vi.waitFor(() => expect(appScope('hello')).toBeDefined());
    await vi.waitFor(() => expect(bundle.apply).toHaveBeenCalled());
    disposeAppScope('hello');
    releaseGate();
    await loading;

    // The disposal cannot take back a node it never saw; the load has to
    // notice that the scope is gone and stop before adding one.
    expect(stylesheets()).toHaveLength(0);
  });

  it('carries the agent a turn names to the wire', async () => {
    // The bundle is where a multi-agent package drives its own agents,
    // so the name has to survive the mapping to the request: a turn that
    // quietly ran the entry agent instead would look like the named
    // agent having nothing to say.
    let ctx: AppContext | undefined;
    bundle.apply.mockImplementation(async (c: AppContext) => {
      ctx = c;
    });
    apiMock.appStartTurn.mockResolvedValue({ run_id: 'r-judge' });

    await ensureAppScope('hello');
    const run = await ctx!.app.send(
      [{ type: 'text', text: 'who goes first?' }],
      {
        agentID: 'judge',
      },
    );

    expect(run).toBe('r-judge');
    expect(apiMock.appStartTurn).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'hello', agent_id: 'judge' }),
    );
  });
});

describe('app event routing', () => {
  it('delivers turn traffic to the application that is subscribed', () => {
    const seen: string[] = [];
    const off = subscribeAppEvents('hello', (ev) => seen.push(ev.type));
    const offOther = subscribeAppEvents('other', () => seen.push('other'));

    dispatchAppEvent('hello', {
      type: UIEventType.stream,
      data: { app_id: 'hello' },
    });
    dispatchAppEvent('other', {
      type: UIEventType.stream,
      data: { app_id: 'other' },
    });
    off();
    offOther();
    dispatchAppEvent('hello', {
      type: UIEventType.turnEnd,
      data: { app_id: 'hello' },
    });

    // The routing decision was made upstream (apps/store, on the id the
    // event names); this only delivers, and a subscriber hears nothing
    // after it unsubscribes.
    expect(seen).toEqual(['stream', 'other']);
  });
});
