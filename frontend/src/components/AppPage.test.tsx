import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppPage } from './AppPage';
import { useAppsStore } from '../apps/store';
import type { AppSummary } from '../apps/store';

// The page's own surface is not what is under test here: the store is
// driven directly and the surfaces below it are stubs, so what renders is
// the page's decision-making — which application is open, and when the
// bundle it loaded stops being the right one.
vi.mock('./AppGallery', () => ({
  AppGallery: () => null,
  AppIcon: () => null,
}));
vi.mock('./AppImportWizard', () => ({ AppImportWizard: () => null }));
vi.mock('./AppChat', () => ({ AppChat: () => null }));
vi.mock('./AppDiagnostics', () => ({ AppDiagnostics: () => null }));
// The view host names the scope it was handed: which scope the page
// renders is what these tests read.
vi.mock('./AppViewHost', () => ({
  AppViewHost: ({ scope }: { scope: { id: string } }) => (
    <div data-testid="view-host" data-scope={scope.id} />
  ),
}));
vi.mock('../lib/api', () => ({
  api: {
    // The list the page reloads on mount: the same two applications the
    // store is seeded with, so opening one keeps rendering its page.
    appList: vi.fn(async () => [
      { id: 'one', name: 'One', enabled: true },
      { id: 'two', name: 'Two', enabled: true },
    ]),
  },
}));

// The scope host is a mock: what the page owes it is the call, not the
// bundle. A load resolves with an empty scope, which is an application
// without a frontend — the page renders on it either way.
const hostMock = vi.hoisted(() => ({
  ensureAppScope: vi.fn(),
  disposeAppScope: vi.fn(),
  appScope: vi.fn(),
}));

vi.mock('../apps/host', () => hostMock);

function summary(over: Partial<AppSummary> & { id: string }): AppSummary {
  return { name: over.id, enabled: true, ...over };
}

beforeEach(() => {
  vi.clearAllMocks();
  hostMock.appScope.mockReturnValue(undefined);
  hostMock.ensureAppScope.mockImplementation(async (id: string) => ({
    id,
    manifest: { app: 'app: v1', id, name: id },
    views: [],
  }));
  useAppsStore.setState({
    apps: [summary({ id: 'one' }), summary({ id: 'two' })],
    openID: '',
    revisions: {},
  });
});

describe('AppPage scope lifetime', () => {
  it('loads the bundle of the application it opens', async () => {
    useAppsStore.setState({ openID: 'one' });
    render(<AppPage />);

    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('one'),
    );
    // Its bundle is not loaded twice, and the other application's is not
    // loaded at all.
    expect(hostMock.ensureAppScope).toHaveBeenCalledTimes(1);
  });

  it('unloads the bundle when the page leaves the application', async () => {
    useAppsStore.setState({ openID: 'one' });
    const { unmount } = render(<AppPage />);
    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('one'),
    );

    unmount();

    // Closing the tool page, stepping back to the gallery and switching
    // between applications all end in this one place: the views, the
    // effects and the stylesheet go with the page that showed them.
    expect(hostMock.disposeAppScope).toHaveBeenCalledWith('one');
  });

  it('unloads the previous bundle when another application is opened', async () => {
    useAppsStore.setState({ openID: 'one' });
    render(<AppPage />);
    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('one'),
    );

    useAppsStore.setState({ openID: 'two' });

    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('two'),
    );
    expect(hostMock.disposeAppScope).toHaveBeenCalledWith('one');
    expect(hostMock.disposeAppScope).not.toHaveBeenCalledWith('two');
  });

  it('unloads and loads again when the registry changes underneath', async () => {
    useAppsStore.setState({ openID: 'one' });
    render(<AppPage />);
    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('one'),
    );
    hostMock.disposeAppScope.mockClear();

    // What the store does on `app_changed`: the bundle the page holds is
    // the changed install's predecessor, so the count is what reloads it.
    useAppsStore.setState({ revisions: { one: 1 } });

    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledTimes(2),
    );
    expect(hostMock.disposeAppScope).toHaveBeenCalledWith('one');
  });

  it('renders only a scope that belongs to the open application', async () => {
    // A scope of the application the page was showing, handed back for
    // the one it is opening, and a load that has not answered yet: this
    // is the frame a switch renders before its own bundle arrives.
    hostMock.appScope.mockReturnValue({
      id: 'one',
      manifest: { app: 'app: v1', id: 'one', name: 'One' },
      views: [
        { id: 'main', title: 'Main', appID: 'one', Component: () => null },
      ],
    });
    hostMock.ensureAppScope.mockReturnValue(new Promise(() => {}));
    useAppsStore.setState({ openID: 'two' });
    render(<AppPage />);

    await waitFor(() =>
      expect(hostMock.ensureAppScope).toHaveBeenCalledWith('two'),
    );

    // One application's views under another one's name is the failure
    // this prevents: the page waits for its own scope instead.
    expect(screen.queryByTestId('view-host')).toBeNull();
  });
});
