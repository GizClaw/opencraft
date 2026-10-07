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

function status(
  recovery: gen.AppRecovery,
  assembly: gen.AppAssembly = { count: 0 },
): gen.AppStatus {
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
    assembly,
  };
}

function seed(recovery: gen.AppRecovery, assembly?: gen.AppAssembly) {
  useAppsStore.setState({
    apps: [{ id: 'hello', name: 'Hello', enabled: true }],
    status: { hello: status(recovery, assembly) },
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

// The assembly row is the panel's other half: how many times this
// process built the application's runtime, which caller did it, and —
// kept readable after the fact — the refusal of the last attempt that
// failed. The vocabulary and the stamps are the host's own, so that a
// user can hold the row next to the log line it came from.
describe('AppDiagnostics assembly row', () => {
  const live: gen.AppRecovery = {
    ran: true,
    at: '2026-09-21T10:00:00Z',
    recovered: 0,
  };

  it('says so when nothing has been assembled', async () => {
    seed(live, { count: 0 });

    render(<AppDiagnostics appID="hello" />);

    expect(await screen.findByTestId('app-assembly')).toHaveTextContent(
      'No runtime has been assembled in this process yet.',
    );
    expect(screen.queryByTestId('app-assembly-error')).not.toBeInTheDocument();
  });

  it('names the count, the caller and the moment', async () => {
    seed(live, {
      count: 3,
      last_reason: 'app_read',
      last_at: '2026-09-21T10:04:05Z',
    });

    render(<AppDiagnostics appID="hello" />);

    const row = await screen.findByTestId('app-assembly');
    expect(row).toHaveTextContent(
      'Assembled 3 times, most recently by app_read at 2026-09-21T10:04:05Z.',
    );
    expect(row).toHaveClass('text-dim');
    expect(screen.queryByTestId('app-assembly-error')).not.toBeInTheDocument();
  });

  it('keeps the refusal readable once the application serves again', async () => {
    seed(live, {
      count: 4,
      last_reason: 'app_reload',
      last_at: '2026-09-21T10:10:00Z',
      last_error: 'layer.yaml: agents: app: graph.yaml: no such file',
      last_error_reason: 'app_reload',
      last_error_at: '2026-09-21T10:05:00Z',
    });

    render(<AppDiagnostics appID="hello" />);

    // The count is the latest attempt; the refusal under it is the one
    // thing a user with the YAML open came here to read.
    expect(await screen.findByTestId('app-assembly')).toHaveTextContent(
      'Assembled 4 times, most recently by app_reload at 2026-09-21T10:10:00Z.',
    );
    expect(screen.getByTestId('app-assembly')).toHaveClass('text-warn');
    const refusal = screen.getByTestId('app-assembly-error');
    expect(refusal).toHaveTextContent(
      'Last refusal — app_reload at 2026-09-21T10:05:00Z',
    );
    expect(refusal).toHaveTextContent(
      'layer.yaml: agents: app: graph.yaml: no such file',
    );
  });
});
