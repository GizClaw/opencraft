import { beforeEach, describe, expect, it, vi } from 'vitest';
import { usePluginStore } from './store';
import type { PluginSummary } from './types';

const apiMock = vi.hoisted(() => ({
  pluginList: vi.fn(),
  pluginBundle: vi.fn(),
  pluginSetEnabled: vi.fn(),
}));

const hostMock = vi.hoisted(() => ({
  resetHost: vi.fn(),
  startHost: vi.fn(),
  activatePlugin: vi.fn(),
  getContributions: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('./host', () => ({
  ...hostMock,
  sortByOrder: (a: { order: number }, b: { order: number }) => a.order - b.order,
}));

function plugin(over: Partial<PluginSummary> & { id: string }): PluginSummary {
  return {
    name: over.id,
    version: '1.0.0',
    entry: 'dist/index.js',
    permissions: [],
    enabled: true,
    ...over,
  };
}

const empty = {
  settingsPanels: [],
  sidebarEntries: [],
  commands: [],
  statusBar: [],
  petPacks: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  usePluginStore.setState({
    plugins: [],
    panels: [],
    entries: [],
    commands: [],
    statusBar: [],
    errors: {},
    loading: false,
  });
  apiMock.pluginBundle.mockResolvedValue('export const apply = () => {}');
  apiMock.pluginSetEnabled.mockResolvedValue(undefined);
  hostMock.getContributions.mockReturnValue({ ...empty });
});

// The reload cycle the plugin settings page runs: list the registry, ask
// for the bundle of every plugin that is enabled and has not failed, and
// collect what they registered. Only the page and the host registrar had
// tests before (against hand-seeded store state), so the load path
// itself — the one place a plugin bundle is fetched and activated — was
// unverified.
describe('usePluginStore.load', () => {
  it('fetches one bundle per enabled plugin and skips the rest', async () => {
    apiMock.pluginList.mockResolvedValue([
      plugin({ id: 'one' }),
      plugin({ id: 'two', enabled: false }),
      plugin({ id: 'three', error: 'manifest invalid' }),
    ]);

    await usePluginStore.getState().load();

    expect(apiMock.pluginBundle).toHaveBeenCalledTimes(1);
    expect(apiMock.pluginBundle).toHaveBeenCalledWith('one');
    expect(hostMock.activatePlugin).toHaveBeenCalledTimes(1);
    expect(hostMock.activatePlugin).toHaveBeenCalledWith('one', expect.any(String), []);
    // A registry entry that failed to load is reported instead of
    // activated silently.
    expect(usePluginStore.getState().errors).toEqual({ three: 'manifest invalid' });
    expect(usePluginStore.getState().plugins.map((p) => p.id)).toEqual([
      'one',
      'two',
      'three',
    ]);
    expect(usePluginStore.getState().loading).toBe(false);
  });

  it('shares one pass between concurrent calls', async () => {
    apiMock.pluginList.mockResolvedValue([plugin({ id: 'one' })]);
    let release: (src: string) => void = () => {};
    apiMock.pluginBundle.mockReturnValue(
      new Promise<string>((resolve) => {
        release = resolve;
      }),
    );

    const first = usePluginStore.getState().load();
    const second = usePluginStore.getState().load();
    expect(first).toBe(second);

    release('export const apply = () => {}');
    await Promise.all([first, second]);

    // StrictMode double-invokes effects, and a refresh can race one:
    // two passes would reset the Cordis app twice and activate every
    // plugin twice, which is what duplicated panels in the UI.
    expect(apiMock.pluginList).toHaveBeenCalledTimes(1);
    expect(hostMock.resetHost).toHaveBeenCalledTimes(1);
    expect(hostMock.activatePlugin).toHaveBeenCalledTimes(1);
    expect(hostMock.startHost).toHaveBeenCalledTimes(1);
  });

  it('runs a fresh pass once the previous one settled', async () => {
    apiMock.pluginList.mockResolvedValue([plugin({ id: 'one' })]);

    await usePluginStore.getState().load();
    await usePluginStore.getState().load();

    expect(apiMock.pluginList).toHaveBeenCalledTimes(2);
    expect(apiMock.pluginBundle).toHaveBeenCalledTimes(2);
  });

  it('publishes the sorted contributions of the pass', async () => {
    apiMock.pluginList.mockResolvedValue([plugin({ id: 'one' })]);
    const first = { order: 1, id: 'b' };
    const second = { order: 0, id: 'a' };
    hostMock.getContributions.mockReturnValue({
      ...empty,
      settingsPanels: [first, second] as never,
      commands: [first, second] as never,
    });

    await usePluginStore.getState().load();

    expect(usePluginStore.getState().panels.map((p) => p.id)).toEqual(['a', 'b']);
    expect(usePluginStore.getState().commands.map((c) => c.id)).toEqual(['a', 'b']);
  });

  it('records a bundle that fails to load and keeps the others', async () => {
    apiMock.pluginList.mockResolvedValue([plugin({ id: 'one' }), plugin({ id: 'two' })]);
    apiMock.pluginBundle.mockImplementation((id: string) =>
      id === 'one'
        ? Promise.reject(new Error('bundle missing'))
        : Promise.resolve('export const apply = () => {}'),
    );

    await usePluginStore.getState().load();

    expect(usePluginStore.getState().errors).toEqual({
      one: expect.stringContaining('bundle missing'),
    });
    expect(hostMock.activatePlugin).toHaveBeenCalledTimes(1);
    expect(hostMock.activatePlugin).toHaveBeenCalledWith('two', expect.any(String), []);
  });
});

describe('usePluginStore.setEnabled', () => {
  it('toggles the plugin through the host and reloads', async () => {
    apiMock.pluginList.mockResolvedValue([plugin({ id: 'one' })]);

    await usePluginStore.getState().setEnabled('one', false);

    expect(apiMock.pluginSetEnabled).toHaveBeenCalledWith('one', false);
    // The reload is what makes the panel list match the new state.
    expect(apiMock.pluginList).toHaveBeenCalledTimes(1);
  });
});
