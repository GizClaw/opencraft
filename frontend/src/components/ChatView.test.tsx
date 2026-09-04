import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { stateRoot } from '../state/app';
import type { MessageView, TurnArtifacts } from '../lib/store';
import { ChatView } from './ChatView';

const apiMock = vi.hoisted(() => ({
  workspace: vi.fn(),
  readAttachment: vi.fn(),
  pickFile: vi.fn(),
  openPath: vi.fn(),
  saveArtifactAs: vi.fn(async () => ''),
  revealArtifact: vi.fn(async () => undefined),
  openArtifactWith: vi.fn(async () => undefined),
  startTurn: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
}));

function manyMessages(n: number): MessageView[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `m-${i}`,
    role: 'user',
    text: `message-${i}`,
    items: [],
    attachments: [],
  }));
}

function setConversation(
  messages: MessageView[],
  turnArtifacts: TurnArtifacts[] = [],
) {
  stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
  const actor = stateRoot.registry.ensure('s-1', {
    workspaceGeneration: stateRoot.generation(),
  });
  actor?.send({ type: 'NEW_CHAT_READY' });
  useStore.setState({
    configured: true,
    workspace: '/tmp/w',
    conversations: {
      's-1': {
        messages,
        turnArtifacts,
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
      },
    },
  });
}

beforeEach(() => {
  stateRoot.resetWorkspace();
  vi.clearAllMocks();
  apiMock.workspace.mockResolvedValue('/tmp/w');
});

describe('ChatView transcript windowing', () => {
  it('renders only the newest 200 messages and loads earlier at the top', () => {
    setConversation(manyMessages(250));
    render(<ChatView />);

    // Oldest messages are not mounted; the tail is.
    expect(screen.queryByText('message-0')).not.toBeInTheDocument();
    expect(screen.getByText('message-50')).toBeInTheDocument();
    expect(screen.getByText('message-249')).toBeInTheDocument();

    const scroller = screen.getByTestId('chat-scroll');
    Object.defineProperty(scroller, 'scrollHeight', {
      configurable: true,
      value: 10_000,
    });
    Object.defineProperty(scroller, 'clientHeight', {
      configurable: true,
      value: 500,
    });
    Object.defineProperty(scroller, 'scrollTop', {
      configurable: true,
      value: 0,
      writable: true,
    });
    fireEvent.scroll(scroller);

    expect(screen.getByText('message-0')).toBeInTheDocument();
    expect(screen.queryByText('message-249')).toBeInTheDocument();
  });

  it('keeps the full transcript when it fits in the window', () => {
    setConversation(manyMessages(10));
    render(<ChatView />);

    expect(screen.getByText('message-0')).toBeInTheDocument();
    expect(screen.getByText('message-9')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /earlier messages/i }),
    ).not.toBeInTheDocument();
  });

  it('renders markdown inside user bubbles', () => {
    setConversation([
      {
        id: 'm-user',
        role: 'user',
        text: '**bold** and `code`',
        items: [],
        attachments: [],
      },
    ]);
    render(<ChatView />);

    expect(screen.getByText('bold').tagName).toBe('STRONG');
    expect(screen.getByText('code').tagName).toBe('CODE');
  });

  it('opens a right-click menu on turn artifacts', async () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'make a file',
          items: [],
          attachments: [],
        },
        { id: 'm-2', role: 'user', text: 'done', items: [], attachments: [] },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [{ path: '/tmp/w/report.md', bytes: 42 }],
        },
      ],
    );
    render(<ChatView />);

    const chip = screen.getByRole('button', { name: /report\.md/i });
    fireEvent.contextMenu(chip);

    expect(
      screen.getByRole('menuitem', { name: 'Save As…' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: 'Copy Path' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: /File Manager/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: 'Open With…' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('separator')).toBeInTheDocument();
    const items = screen.getAllByRole('menuitem');
    expect(items[0]).toHaveTextContent('Open With…');
    expect(items[1]).toHaveTextContent('Save As…');
    expect(items[2]).toHaveTextContent('Copy Path');
    expect(items[3]).toHaveTextContent(/File Manager/i);

    await userEvent
      .setup()
      .click(screen.getByRole('menuitem', { name: 'Save As…' }));

    expect(apiMock.saveArtifactAs).toHaveBeenCalledWith('/tmp/w/report.md');
    expect(
      screen.queryByRole('menuitem', { name: 'Save As…' }),
    ).not.toBeInTheDocument();
  });

  it('shows worked duration centered under the turn artifacts', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'make a file',
          items: [],
          attachments: [],
        },
        { id: 'm-2', role: 'user', text: 'done', items: [], attachments: [] },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [{ path: '/tmp/w/report.md', bytes: 42 }],
          durationMs: 3723000,
        },
      ],
    );
    render(<ChatView />);

    expect(screen.getByText('Worked for 1h 2m 3s')).toBeInTheDocument();
  });

  it('shows worked duration even when the turn produced no artifacts', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'do something',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'done' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          durationMs: 123000,
        },
      ],
    );
    render(<ChatView />);

    expect(screen.queryByText('Produced this turn')).not.toBeInTheDocument();
    expect(screen.getByText('Worked for 2m 3s')).toBeInTheDocument();
  });

  it('does not infer worked duration from second-precision timestamps', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'do something',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'done' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          startedAt: '2026-09-04T12:00:00Z',
          finishedAt: '2026-09-04T12:00:00Z',
        },
      ],
    );
    render(<ChatView />);

    expect(screen.queryByText(/Worked for/)).not.toBeInTheDocument();
  });

  it('renders sub-second durations as <1s', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'do something',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'done' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          durationMs: 500,
        },
      ],
    );
    render(<ChatView />);

    expect(screen.getByText('Worked for <1s')).toBeInTheDocument();
  });

  it('renders message-peek ticks with user/answer previews', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'build search',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'search built' }],
          attachments: [],
        },
        {
          id: 'm-3',
          role: 'user',
          text: 'add sorting',
          items: [],
          attachments: [],
        },
      ],
      [
        { id: 'turn-1', start: 0, docs: [] },
        { id: 'turn-2', start: 2, docs: [] },
      ],
    );
    render(<ChatView />);

    expect(screen.getByTestId('message-peek')).toBeInTheDocument();
    const firstTick = screen.getByRole('button', {
      name: 'Jump to turn 1',
    });
    fireEvent.mouseEnter(firstTick);

    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('build search');
    expect(tooltip).toHaveTextContent('search built');
  });
});

describe('ChatView projections', () => {
  it('renders the greeting above the centered composer in a new session', () => {
    setConversation([]);
    render(<ChatView />);
    expect(screen.getByText('What shall we craft today?')).toBeInTheDocument();
  });

  it('shows the think level when the auto router model supports reasoning', () => {
    setConversation([]);
    useStore.setState({
      modelOptions: [
        {
          id: 'deepseek-1/deepseek-v4-flash',
          label: 'Primary',
          reasoning: true,
        },
      ],
    });
    render(<ChatView />);
    expect(screen.getByText('Medium')).toBeInTheDocument();
  });

  it('renders no-session with a start action', () => {
    stateRoot.resetWorkspace();
    render(<ChatView />);
    expect(
      screen.getByRole('button', { name: 'Start a new conversation' }),
    ).toBeInTheDocument();
  });

  it('renders an opening placeholder while focus is switching', () => {
    stateRoot.resetWorkspace();
    stateRoot.sendFocus({ type: 'OPEN_SESSION', id: 's-2' });
    render(<ChatView />);
    expect(screen.getByText('Opening conversation…')).toBeInTheDocument();
  });

  it('renders switch failure with back and retry actions', () => {
    stateRoot.resetWorkspace();
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    stateRoot.sendFocus({ type: 'OPEN_SESSION', id: 's-2' });
    stateRoot.sendFocus({ type: 'OPEN_FAILED', request: 1, error: 'boom' });
    render(<ChatView />);

    expect(
      screen.getByText("Couldn't open that conversation"),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back' })).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Try again' }),
    ).toBeInTheDocument();
  });

  it('renders transcript loading while history is hydrating', () => {
    setConversation([]);
    stateRoot.resetWorkspace();
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    stateRoot.registry
      .ensure('s-1', {
        workspaceGeneration: stateRoot.generation(),
      })
      ?.send({
        type: 'HYDRATE_REQUESTED',
        request: 99,
        generation: stateRoot.generation(),
      });
    render(<ChatView />);
    expect(screen.getByText('Loading history…')).toBeInTheDocument();
  });
});
