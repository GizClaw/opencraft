import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppDiagnostics } from './AppDiagnostics';
import { useAppsStore } from '../apps/store';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';

// The panel is where a broken application sends you, so what it says
// about the application's own state root is the point: an application's
// sessions live under the same kind of root a workspace's do, and the
// host recovers them the same way — but the sentence has to be the
// application's. "Another live process holds this workspace" would name
// an internal path the user never picked (app platform plan, §3.7).

const apiMock = vi.hoisted(() => ({
  appFiles: vi.fn(async () => []),
  appReadFile: vi.fn(async () => ''),
  appReveal: vi.fn(async () => undefined),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('./AppGallery', () => ({ AppIcon: () => null }));

function status(recovery: gen.AppRecovery): gen.AppStatus {
  return {
    id: 'hello',
    name: 'Hello',
    enabled: true,
    builtin: false,
    serving: true,
    retiring: false,
    content_root: '/apps/hello/content',
    state_root: '/data/apps/hello',
    work_dir: '/data/apps/hello/workspace',
    recovery,
  };
}

function seed(recovery: gen.AppRecovery) {
  useAppsStore.setState({
    apps: [{ id: 'hello', name: 'Hello', enabled: true }],
    status: { hello: status(recovery) },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('AppDiagnostics recovery line', () => {
  it('names the live process that owns the application, not a workspace', async () => {
    seed({
      ran: false,
      at: '2026-09-21T10:00:00Z',
      recovered: 0,
      holder: 'pid 4242 (headless)',
    });

    render(<AppDiagnostics appID="hello" />);

    const line = await screen.findByTestId('app-recovery');
    expect(line).toHaveTextContent(
      'Another live process owns this application (pid 4242 (headless)); this process ran no recovery.',
    );
    expect(line).not.toHaveTextContent(/workspace/i);
    // The application's name is what the panel calls it, and the state
    // root it lives in is shown as a path, never as the name.
    expect(screen.getByText('Hello')).toBeInTheDocument();
    expect(screen.getByText('/data/apps/hello')).toBeInTheDocument();
  });

  it('counts the turns the pass brought back', async () => {
    seed({ ran: true, at: '2026-09-21T10:00:00Z', recovered: 2 });

    render(<AppDiagnostics appID="hello" />);

    expect(await screen.findByTestId('app-recovery')).toHaveTextContent(
      'This process recovered 2 unfinished turns as interrupted.',
    );
  });

  it('says the pass was clean when nothing was left behind', async () => {
    seed({ ran: true, at: '2026-09-21T10:00:00Z', recovered: 0 });

    render(<AppDiagnostics appID="hello" />);

    expect(await screen.findByTestId('app-recovery')).toHaveTextContent(
      'This process ran a crash pass and found no unarchived turns.',
    );
  });

  it('admits it has not looked when no runtime reported anything', async () => {
    seed({ ran: false, recovered: 0 });

    render(<AppDiagnostics appID="hello" />);

    expect(await screen.findByTestId('app-recovery')).toHaveTextContent(
      "This process has not looked at this application's state yet.",
    );
  });
});
