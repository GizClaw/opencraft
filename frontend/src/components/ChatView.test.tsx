import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import type { MessageView } from '../lib/store';
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

function setConversation(messages: MessageView[]) {
  useStore.setState({
    configured: true,
    current: 's-1',
    workspace: '/tmp/w',
    conversations: {
      's-1': {
        messages,
        turnArtifacts: [],
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
  it('renders only the newest 200 messages and loads earlier on demand', async () => {
    setConversation(manyMessages(250));
    render(<ChatView />);

    // Oldest messages are not mounted; the tail is.
    expect(screen.queryByText('message-0')).not.toBeInTheDocument();
    expect(screen.getByText('message-50')).toBeInTheDocument();
    expect(screen.getByText('message-249')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /earlier messages/i }));

    expect(screen.getByText('message-0')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /earlier messages/i }),
    ).not.toBeInTheDocument();
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
});
