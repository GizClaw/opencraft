import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useAppsStore, eventAppID } from './store';
import { UIEventType } from '../lib/events';
import type { AppSummary } from './store';

const apiMock = vi.hoisted(() => ({
  appList: vi.fn(),
  appStatus: vi.fn(),
  appSetEnabled: vi.fn(),
  appUninstall: vi.fn(),
  appRollback: vi.fn(),
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
    recovery: { ran: true, recovered: 0 },
    assembly: { count: 1, last_reason: 'app_enable' },
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
    events: {},
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

// The panel's recent-events list is the page's own record, so what it
// holds is what the page heard: every event that names an application,
// under the application it names, newest first.
describe('recent events', () => {
  const ring = (id: string) => useAppsStore.getState().events[id] ?? [];

  it('records the events one application caused, newest first', () => {
    const store = useAppsStore.getState();
    store.handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one' },
    });
    store.handleEvent({
      type: UIEventType.appStatus,
      data: { id: 'one', serving: true },
    });
    store.handleEvent({
      type: UIEventType.artifact,
      data: { app_id: 'one', run_id: 'r1', path: 'out/hello.txt' },
    });

    expect(ring('one').map((e) => [e.type, e.detail])).toEqual([
      [UIEventType.artifact, 'out/hello.txt'],
      [UIEventType.appStatus, 'serving'],
      [UIEventType.appChanged, ''],
    ]);
    expect(ring('one')[0]?.at).toBeGreaterThan(0);
  });

  it('spells out the two things a payload decides', () => {
    const store = useAppsStore.getState();
    store.handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one', assets: true },
    });
    store.handleEvent({
      type: UIEventType.appEvent,
      data: { app_id: 'one', subject: 'app.one.turn', payload: {} },
    });
    store.handleEvent({
      type: UIEventType.appStatus,
      data: { id: 'one', serving: false, retiring: true },
    });

    expect(ring('one').map((e) => e.detail)).toEqual([
      'retiring',
      'app.one.turn',
      'assets',
    ]);
  });

  it('keeps one ring per application and bounds it', () => {
    const store = useAppsStore.getState();
    store.handleEvent({
      type: UIEventType.turnEnd,
      data: { app_id: 'two', run_id: 'r1', status: 'completed' },
    });
    for (let i = 0; i < 14; i += 1) {
      store.handleEvent({
        type: UIEventType.appStatus,
        data: { id: 'one', serving: true },
      });
    }

    // The other application's event is its own: a page showing one
    // application must not read another's history as its own.
    expect(ring('two')).toHaveLength(1);
    expect(ring('two')[0]?.detail).toBe('completed');
    // Ten rows is the whole ring, and the newest is the one kept.
    expect(ring('one')).toHaveLength(10);
    expect(ring('one').every((e) => e.type === UIEventType.appStatus)).toBe(
      true,
    );
  });

  it('leaves a stream out of the ring', () => {
    const store = useAppsStore.getState();
    store.handleEvent({
      type: UIEventType.stream,
      data: { app_id: 'one', run_id: 'r1' },
    });
    store.handleEvent({
      type: UIEventType.turnEnd,
      data: { app_id: 'one', run_id: 'r1', status: 'completed' },
    });

    // A reply's deltas are hundreds of rows saying one thing; the event
    // that says it stopped is the one worth keeping.
    expect(ring('one').map((e) => e.type)).toEqual([UIEventType.turnEnd]);
    // The delta still reached the application's scope, which is what
    // the page's chat renders from.
    expect(hostMock.dispatchAppEvent).toHaveBeenCalledWith(
      'one',
      expect.objectContaining({ type: UIEventType.stream }),
    );
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
      events: {
        one: [{ type: UIEventType.appChanged, detail: '', at: 1 }],
      },
    });

    await useAppsStore.getState().uninstall('one', true);

    expect(apiMock.appUninstall).toHaveBeenCalledWith('one', true);
    // An uninstalled application has no runtime and no page to show; a
    // status left behind would keep drawing its card as if it did, and
    // a ring left behind would open the panel of a reinstalled id on
    // the history of the install that left.
    expect(useAppsStore.getState().openID).toBe('');
    expect(useAppsStore.getState().status).toEqual({});
    expect(useAppsStore.getState().events).toEqual({});
    expect(useAppsStore.getState().busy['one']).toBe(false);
  });

  it('reloads one application through its own binding', async () => {
    await useAppsStore.getState().reloadApp('one');

    expect(apiMock.appReload).toHaveBeenCalledWith('one');
    expect(useAppsStore.getState().status['one']?.serving).toBe(true);
  });

  it('re-reads the registry even when a rollback fails', async () => {
    apiMock.appList.mockResolvedValue([
      summary({ id: 'one', version: '1.0.0' }),
    ]);
    // The failure the card has to survive: the registry moved — the
    // snapshot is consumed and the version it held is installed — and the
    // assembly of that version refused. The store cannot tell the two
    // apart from the call, so it re-reads on the way out either way.
    apiMock.appRollback.mockRejectedValue(
      new Error('"one" is restored but cannot be served: no inference'),
    );

    await expect(useAppsStore.getState().rollbackApp('one')).rejects.toThrow(
      /cannot be served/,
    );

    expect(apiMock.appRollback).toHaveBeenCalledWith('one');
    expect(apiMock.appList).toHaveBeenCalledTimes(1);
    expect(apiMock.appStatus).toHaveBeenCalledWith('one');
    expect(useAppsStore.getState().apps[0]?.version).toBe('1.0.0');
    expect(useAppsStore.getState().busy['one']).toBe(false);
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

  it('reloads the bundle without re-reading the registry for an asset edit', () => {
    useAppsStore.setState({ openID: 'one' });

    useAppsStore.getState().handleEvent({
      type: UIEventType.appChanged,
      data: { id: 'one', assets: true },
    });

    // The backend edited the application's bundle and deliberately left
    // its runtime standing: the page reloads the module (the revision),
    // and the list — which such an edit cannot have changed — is not
    // read again. A page whose application is switched off stays open
    // for the same reason: nothing about the registry moved.
    expect(useAppsStore.getState().revisions['one']).toBe(1);
    expect(apiMock.appList).not.toHaveBeenCalled();
    expect(apiMock.appStatus).not.toHaveBeenCalled();
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
