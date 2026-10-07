import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useAppsStore, eventAppID } from './store';
import { UIEventType } from '../lib/events';
import type { AppSummary } from './store';

const apiMock = vi.hoisted(() => ({
  appList: vi.fn(),
  appStatus: vi.fn(),
  appSetEnabled: vi.fn(),
  appUninstall: vi.fn(),
  appReload: vi.fn(),
  appReveal: vi.fn(),
}));

const hostMock = vi.hoisted(() => ({ dispatchAppEvent: vi.fn() }));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('./host', () => hostMock);

function summary(over: Partial<AppSummary> & { id: string }): AppSummary {
  return { name: over.id, enabled: true, ...over };
}

function status(id: string) {
  return {
    id,
    name: id,
    enabled: true,
    builtin: false,
    serving: true,
    retiring: false,
    content_root: `/apps/${id}/content`,
    state_root: `/data/apps/${id}`,
    work_dir: `/data/apps/${id}/workspace`,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  useAppsStore.setState({
    apps: [],
    loading: false,
    error: '',
    openID: '',
    status: {},
    busy: {},
    revisions: {},
  });
  apiMock.appList.mockResolvedValue([]);
  apiMock.appStatus.mockResolvedValue(status('one'));
});

describe('eventAppID', () => {
  it('reads the application an event names', () => {
    expect(
      eventAppID({ type: UIEventType.stream, data: { app_id: 'hi' } }),
    ).toBe('hi');
    // Every workspace event, and a malformed one, belongs to the
    // assistant's side.
    expect(eventAppID({ type: UIEventType.stream, data: {} })).toBe('');
    expect(eventAppID({ type: UIEventType.stream, data: null })).toBe('');
  });
});

describe('useAppsStore.load', () => {
  it('shares one read between concurrent callers', async () => {
    let release: (apps: AppSummary[]) => void = () => {};
    apiMock.appList.mockReturnValue(
      new Promise<AppSummary[]>((resolve) => {
        release = resolve;
      }),
    );

    const first = useAppsStore.getState().load();
    const second = useAppsStore.getState().load();
    expect(first).toBe(second);

    release([summary({ id: 'one' })]);
    await Promise.all([first, second]);

    // StrictMode double-invokes effects, and an install reloads the list
    // right after its own call: two reads would let the slower one win.
    expect(apiMock.appList).toHaveBeenCalledTimes(1);
    expect(useAppsStore.getState().apps.map((a) => a.id)).toEqual(['one']);
    expect(useAppsStore.getState().loading).toBe(false);
  });

  it('reports a list failure without dropping what is already shown', async () => {
    useAppsStore.setState({ apps: [summary({ id: 'one' })] });
    apiMock.appList.mockRejectedValue(new Error('registry unavailable'));

    await useAppsStore.getState().load();

    expect(useAppsStore.getState().error).toContain('registry unavailable');
    expect(useAppsStore.getState().apps.map((a) => a.id)).toEqual(['one']);
    expect(useAppsStore.getState().loading).toBe(false);
  });
});

describe('useAppsStore registry actions', () => {
  it('re-reads both the list and the status after a toggle', async () => {
    apiMock.appList.mockResolvedValue([summary({ id: 'one', enabled: false })]);
    apiMock.appStatus.mockResolvedValue(status('one'));

    await useAppsStore.getState().setEnabled('one', false);

    expect(apiMock.appSetEnabled).toHaveBeenCalledWith('one', false);
    expect(apiMock.appList).toHaveBeenCalledTimes(1);
    expect(apiMock.appStatus).toHaveBeenCalledWith('one');
    expect(useAppsStore.getState().apps[0]?.enabled).toBe(false);
    expect(useAppsStore.getState().busy['one']).toBe(false);
  });

  it('closes the open page and drops the cached status on uninstall', async () => {
    useAppsStore.setState({
      apps: [summary({ id: 'one' })],
      openID: 'one',
      status: { one: status('one') },
    });

    await useAppsStore.getState().uninstall('one', true);

    expect(apiMock.appUninstall).toHaveBeenCalledWith('one', true);
    // An uninstalled application has no runtime and no page to show; a
    // status left behind would keep drawing its card as if it did.
    expect(useAppsStore.getState().openID).toBe('');
    expect(useAppsStore.getState().status).toEqual({});
    expect(useAppsStore.getState().busy['one']).toBe(false);
  });

  it('reloads one application through its own binding', async () => {
    await useAppsStore.getState().reloadApp('one');

    expect(apiMock.appReload).toHaveBeenCalledWith('one');
    expect(useAppsStore.getState().status['one']?.serving).toBe(true);
  });
});

describe('useAppsStore.handleEvent', () => {
  it('re-reads the list and the status on app_changed', async () => {
    apiMock.appList.mockResolvedValue([summary({ id: 'one' })]);

    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });
    await vi.waitFor(() => expect(apiMock.appList).toHaveBeenCalled());
    await vi.waitFor(() => expect(apiMock.appStatus).toHaveBeenCalled());

    expect(useAppsStore.getState().apps.map((a) => a.id)).toEqual(['one']);
  });

  it('closes the page when the registry no longer has the application', async () => {
    useAppsStore.setState({ openID: 'gone' });
    apiMock.appList.mockResolvedValue([summary({ id: 'one' })]);

    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'gone' },
    });

    // The removal is decided from the list, not from the event: the
    // event says "something changed", and the list is the fact.
    await vi.waitFor(() => expect(useAppsStore.getState().openID).toBe(''));
  });

  it('bumps the revision so an open page unloads its bundle', async () => {
    apiMock.appList.mockResolvedValue([summary({ id: 'one' })]);

    expect(useAppsStore.getState().revisions['one']).toBeUndefined();
    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });

    // The loaded bundle is the changed install's predecessor: the page
    // reloads on the count, and only the application the event names is
    // reloaded (an install of another one is not this page's news).
    expect(useAppsStore.getState().revisions['one']).toBe(1);
    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });
    expect(useAppsStore.getState().revisions['one']).toBe(2);
    expect(useAppsStore.getState().revisions['two']).toBeUndefined();
    await vi.waitFor(() => expect(apiMock.appList).toHaveBeenCalled());
  });

  it('closes the page of an application that was switched off', async () => {
    useAppsStore.setState({ openID: 'one' });
    apiMock.appList.mockResolvedValue([summary({ id: 'one', enabled: false })]);

    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });

    // A disabled application refuses turns (`ErrAppNotEnabled`), so a
    // page left open on one is a composer that cannot send.
    await vi.waitFor(() => expect(useAppsStore.getState().openID).toBe(''));
  });

  it('keeps the page of an application the list still has', async () => {
    useAppsStore.setState({ openID: 'one' });
    apiMock.appList.mockResolvedValue([summary({ id: 'one' })]);

    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });

    await vi.waitFor(() => expect(apiMock.appList).toHaveBeenCalled());
    expect(useAppsStore.getState().openID).toBe('one');
  });

  it('refreshes one status on app_status', () => {
    useAppsStore.getState().handleEvent({
      type: UIEventType.appStatus,
      data: { id: 'one', serving: true },
    });

    // A status event is a decoration: it re-reads instead of trusting
    // the payload, so the card cannot drift from the pool.
    expect(apiMock.appStatus).toHaveBeenCalledWith('one');
    expect(apiMock.appList).not.toHaveBeenCalled();
  });

  it('hands turn events to the named application only', () => {
    useAppsStore.getState().handleEvent({
      type: UIEventType.stream,
      data: { app_id: 'one', run_id: 'r1' },
    });
    useAppsStore.getState().handleEvent({
      type: UIEventType.turnEnd,
      data: { conversation_id: 's-1', run_id: 'r1' },
    });

    expect(hostMock.dispatchAppEvent).toHaveBeenCalledTimes(1);
    expect(hostMock.dispatchAppEvent).toHaveBeenCalledWith(
      'one',
      expect.objectContaining({ type: UIEventType.stream }),
    );
  });
});
