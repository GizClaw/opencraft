import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import type { MessageView, TurnArtifacts } from '../lib/store';
import { ChatView } from './ChatView';

const apiMock = vi.hoisted(() => ({
  projectConfigStatus: vi.fn(),
  workspace: vi.fn(),
  setProjectTrust: vi.fn(),
  undoState: vi.fn(),
  undoChange: vi.fn(),
  redoChange: vi.fn(),
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
  useStore.setState({
    configured: true,
    current: 's-1',
    workspace: '/tmp/w',
    conversations: {
      's-1': {
        messages,
        turnArtifacts,
        busy: false,
        activeRunID: null,
        stage: '',
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
        lastFailed: false,
      },
    },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.projectConfigStatus.mockResolvedValue(null);
  apiMock.workspace.mockResolvedValue('/tmp/w');
  apiMock.undoState.mockResolvedValue({ can_undo: false, can_redo: false });
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
});
