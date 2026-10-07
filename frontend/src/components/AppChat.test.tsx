import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppChat } from './AppChat';
import { UIEventType } from '../lib/events';
import type { UIEvent } from '../lib/types';
import type { Manifest } from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';

// The application's stream is one backend channel per application, not
// per conversation, so what this component renders depends on the guards
// it puts on the events it receives. Driving them by hand is how those
// guards are pinned.
const hostMock = vi.hoisted(() => ({
  subscribeAppEvents: vi.fn(),
  subscribeAppSubject: vi.fn(),
}));

const apiMock = vi.hoisted(() => ({
  appSessions: vi.fn(),
  appHistory: vi.fn(),
  appActiveRun: vi.fn(),
  appStartTurn: vi.fn(),
  appCancel: vi.fn(),
  appReveal: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../apps/host', () => hostMock);

/** listeners are the callbacks the component subscribed with. */
let listeners: ((ev: UIEvent) => void)[] = [];

/** emit delivers one backend event to the page's subscription. */
function emit(type: string, data: Record<string, unknown>) {
  for (const listener of [...listeners]) listener({ type, data });
}

/**
 * flush lets the render the emit caused commit. A state update outside
 * React's own event handling is batched until the next tick, so a test
 * that only asserts *absence* has to wait for the render that would have
 * produced the thing it is looking for — otherwise it passes whether the
 * component ignored the event or simply had not rendered yet.
 */
async function flush() {
  await act(async () => {});
}

const manifest = {
  app: 'app: v1',
  id: 'hello',
  name: 'Hello',
  defaults: { model: 'test-model', think_level: 'low' },
} as unknown as Manifest;

beforeEach(() => {
  vi.clearAllMocks();
  listeners = [];
  hostMock.subscribeAppEvents.mockImplementation(
    (_id: string, cb: (ev: UIEvent) => void) => {
      listeners.push(cb);
      return () => {
        listeners = listeners.filter((l) => l !== cb);
      };
    },
  );
  apiMock.appSessions.mockResolvedValue([{ id: 's-1' }]);
  apiMock.appHistory.mockResolvedValue([]);
  apiMock.appActiveRun.mockResolvedValue('');
  apiMock.appStartTurn.mockResolvedValue({
    run_id: 'r1',
    conversation_id: 's-1',
  });
});

describe('AppChat event attribution', () => {
  it('keeps another conversation deltas out of this transcript', async () => {
    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());

    // A run an application's own scripts started, or one the page just
    // left: same application, another conversation.
    emit(UIEventType.stream, {
      app_id: 'hello',
      run_id: 'r-other',
      conversation_id: 's-2',
      delta: { part: { type: 'text', text: 'not this page' } },
    });
    await flush();
    expect(screen.queryByText('not this page')).toBeNull();

    emit(UIEventType.stream, {
      app_id: 'hello',
      run_id: 'r1',
      conversation_id: 's-1',
      delta: { part: { type: 'text', text: 'this page' } },
    });
    expect(await screen.findByText('this page')).toBeTruthy();
  });

  it('shows the files of this conversation only', async () => {
    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());

    emit(UIEventType.artifact, {
      app_id: 'hello',
      run_id: 'r-other',
      conversation_id: 's-2',
      path: 'other.txt',
    });
    await flush();
    expect(screen.queryByTestId('app-artifacts')).toBeNull();

    emit(UIEventType.artifact, {
      app_id: 'hello',
      run_id: 'r1',
      conversation_id: 's-1',
      path: 'hello.txt',
    });
    expect(await screen.findByText('hello.txt')).toBeTruthy();
  });

  it('ends the turn whose end arrives before the start call answers', async () => {
    const user = userEvent.setup();
    // A turn that fails immediately: its end can reach the page before
    // the send's own answer does, and the page knows neither id yet.
    apiMock.appStartTurn.mockImplementation(async () => {
      emit(UIEventType.turnEnd, {
        app_id: 'hello',
        run_id: 'r1',
        conversation_id: 's-1',
        status: 'failed',
        error: 'provider refused',
      });
      return { run_id: 'r1', conversation_id: 's-1' };
    });

    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());
    await user.type(screen.getByTestId('app-composer'), 'hello');
    await user.click(screen.getByText('Send'));

    // The notice is what says the turn is over; a page that ignored the
    // event would keep showing a run that has already ended.
    expect(await screen.findByTestId('app-turn-notice')).toBeTruthy();
    expect(screen.getByText('provider refused')).toBeTruthy();
  });

  it('ignores the end of another run while one is in flight', async () => {
    const user = userEvent.setup();
    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());
    await user.type(screen.getByTestId('app-composer'), 'hello');
    await user.click(screen.getByText('Send'));
    await waitFor(() => expect(apiMock.appStartTurn).toHaveBeenCalled());

    emit(UIEventType.turnEnd, {
      app_id: 'hello',
      run_id: 'r-other',
      conversation_id: 's-1',
      status: 'failed',
      error: 'somebody else',
    });
    await flush();

    // This page's own run is still going: its stop button stays, and the
    // other run's failure is not reported as this one's.
    expect(screen.queryByTestId('app-turn-notice')).toBeNull();
    expect(screen.getByText('Stop')).toBeTruthy();
  });

  it('ignores the end of a turn in another conversation', async () => {
    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());

    // The page is on the newest conversation with nothing of its own
    // running; a turn of another one (a scheduled script of the
    // application, a run left over from a session this page is not
    // showing) is not this page's news.
    emit(UIEventType.turnEnd, {
      app_id: 'hello',
      run_id: 'r-other',
      conversation_id: 's-2',
      status: 'failed',
      error: 'somebody else',
    });
    await flush();

    expect(screen.queryByTestId('app-turn-notice')).toBeNull();
  });

  it('starts the next turn with an empty file strip', async () => {
    const user = userEvent.setup();
    render(<AppChat appID="hello" manifest={manifest} />);
    await waitFor(() => expect(apiMock.appSessions).toHaveBeenCalled());
    emit(UIEventType.artifact, {
      app_id: 'hello',
      run_id: 'r0',
      conversation_id: 's-1',
      path: 'previous.txt',
    });
    expect(await screen.findByText('previous.txt')).toBeTruthy();

    await user.type(screen.getByTestId('app-composer'), 'again');
    await user.click(screen.getByText('Send'));

    // The strip is the turn's, so the files of the turn before it are not
    // still standing under the one that just started.
    expect(screen.queryByText('previous.txt')).toBeNull();
  });
});
