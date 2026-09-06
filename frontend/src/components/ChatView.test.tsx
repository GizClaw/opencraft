import { act, fireEvent, render, screen, within } from '@testing-library/react';
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

    const scroller = screen.getByTestId('chat-scroll');
    // Oldest messages are not mounted; the tail is.
    expect(within(scroller).queryByText('message-0')).not.toBeInTheDocument();
    expect(within(scroller).getByText('message-50')).toBeInTheDocument();
    expect(within(scroller).getByText('message-249')).toBeInTheDocument();

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

    expect(within(scroller).getByText('message-0')).toBeInTheDocument();
    expect(within(scroller).queryByText('message-249')).toBeInTheDocument();
  });

  it('keeps the full transcript when it fits in the window', () => {
    setConversation(manyMessages(10));
    render(<ChatView />);

    const scroller = screen.getByTestId('chat-scroll');
    expect(within(scroller).getByText('message-0')).toBeInTheDocument();
    expect(within(scroller).getByText('message-9')).toBeInTheDocument();
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
    expect(items[0]).toHaveTextContent('Open in viewer');
    expect(items[1]).toHaveTextContent('Open With…');
    expect(items[2]).toHaveTextContent('Save As…');
    expect(items[3]).toHaveTextContent('Copy Path');
    expect(items[4]).toHaveTextContent(/File Manager/i);

    await userEvent
      .setup()
      .click(screen.getByRole('menuitem', { name: 'Save As…' }));

    expect(apiMock.saveArtifactAs).toHaveBeenCalledWith('/tmp/w/report.md');
    expect(
      screen.queryByRole('menuitem', { name: 'Save As…' }),
    ).not.toBeInTheDocument();
  });

  it('shows a staged queue draft above the composer and can cancel it', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'prompt',
          items: [],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    expect(useStore.getState().queueInput('staged question')).toBe(true);

    render(<ChatView />);

    expect(screen.getByText(/staged question/)).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Cancel queued message' }),
    );
    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
    expect(screen.queryByText(/staged question/)).not.toBeInTheDocument();
  });

  it('Enter with an empty composer delivers a draft kept after a failed turn', async () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    const conv = useStore.getState().conversations['s-1'];
    useStore.setState({
      conversations: {
        's-1': {
          ...conv,
          queued: {
            text: 'staged after fail',
            attachments: [],
            interrupt: false,
          },
        },
      },
    });
    actor?.send({
      type: 'TURN_ENDED',
      runID: 'r-old',
      status: 'failed',
      error: 'engine boom',
    });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-queued',
      context_id: 's-1',
    });

    render(<ChatView />);
    expect(screen.getByText(/press Enter to send/i)).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('{Enter}');

    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({
        content: {
          parts: [
            expect.objectContaining({
              type: 'text',
              text: 'staged after fail',
            }),
          ],
        },
      }),
    );
  });

  it('hints Enter/Tab behavior while typing during a running turn', async () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('next question');
    expect(
      screen.getByText(/Enter interrupts the reply · Tab queues the message/i),
    ).toBeInTheDocument();

    // Tab stages the draft and clears the composer, which hides the
    // hint and surfaces the queue banner instead.
    await userEvent.keyboard('{Tab}');
    expect(
      screen.queryByText(
        /Enter interrupts the reply · Tab queues the message/i,
      ),
    ).not.toBeInTheDocument();
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'next question',
      interrupt: false,
    });
  });

  it('shows Stop only for an empty composer and Send once a running-turn draft exists', async () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-next',
      context_id: 's-1',
    });
    render(<ChatView />);

    // While the agent runs with nothing typed, the primary action is Stop.
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Send' }),
    ).not.toBeInTheDocument();

    // Typing a draft restores Send; the button barges in like Enter.
    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('next question');
    expect(
      screen.queryByRole('button', { name: 'Stop' }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole('button', { name: 'Send' }));
    await vi.waitFor(() => expect(apiMock.startTurn).toHaveBeenCalled());
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({
        content: {
          parts: [
            expect.objectContaining({
              type: 'text',
              text: 'next question',
            }),
          ],
        },
      }),
    );
    // The draft was cleared by the interrupt, so Stop is primary again.
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Send' }),
    ).not.toBeInTheDocument();
  });

  it('renders recognizable badges for common document attachments', async () => {
    const attachments: MessageView['attachments'] = [
      {
        id: 'att-pdf',
        kind: 'file',
        path: '/tmp/report.pdf',
        name: 'report.pdf',
        media_type: 'application/pdf',
      },
      {
        id: 'att-doc',
        kind: 'file',
        path: '/tmp/note.docx',
        name: 'note.docx',
        media_type:
          'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      },
      {
        id: 'att-xls',
        kind: 'file',
        path: '/tmp/table.xlsx',
        name: 'table.xlsx',
        media_type:
          'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      },
      {
        id: 'att-ppt',
        kind: 'file',
        path: '/tmp/deck.pptx',
        name: 'deck.pptx',
        media_type:
          'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      },
      {
        id: 'att-md',
        kind: 'file',
        path: '/tmp/readme.md',
        name: 'readme.md',
        media_type: 'text/markdown',
      },
    ];
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'summarize these',
          items: [],
          attachments,
        },
      ],
      [],
    );
    render(<ChatView />);

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: '5 files' }));
    for (const badge of ['PDF', 'DOC', 'XLS', 'PPT', 'MD']) {
      expect(screen.getByText(badge)).toBeInTheDocument();
    }
  });

  it('renders images above the bubble and opens files in a floating list', async () => {
    const attachments: MessageView['attachments'] = [
      {
        id: 'att-img',
        kind: 'image',
        path: '/tmp/pic.png',
        name: 'pic.png',
        media_type: 'image/png',
        data_url: 'data:image/png;base64,AAAA',
      },
      ...Array.from({ length: 5 }, (_, i) => ({
        id: `att-f${i}`,
        kind: 'file' as const,
        path: `/tmp/file-${i}.md`,
        name: `file-${i}.md`,
        media_type: 'text/markdown',
      })),
    ];
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'check these',
          items: [],
          attachments,
        },
      ],
      [],
    );
    render(<ChatView />);

    const user = userEvent.setup();
    const text = document.querySelector('.rounded-2xl p');
    expect(text).not.toBeNull();
    const bubble = text!.closest('.rounded-2xl');
    expect(bubble).not.toBeNull();

    // Images render above the bubble, the file chip below it, and
    // neither keeps a bubble background behind it.
    const image = screen.getByRole('img', { name: 'pic.png' });
    expect(image.closest('.rounded-2xl')).toBeNull();
    expect(
      image.compareDocumentPosition(text!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    const fileChip = screen.getByRole('button', { name: '5 files' });
    expect(fileChip.closest('.rounded-2xl')).toBeNull();
    expect(
      text!.compareDocumentPosition(fileChip) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    await user.click(fileChip);
    expect(screen.getAllByRole('menuitem')).toHaveLength(5);
    await user.click(document.body);
    expect(screen.queryByRole('menuitem')).not.toBeInTheDocument();
  });

  it('shows worked duration at the top of an artifact turn', () => {
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
    expect(
      screen.queryByRole('button', { name: 'Worked for 2m 3s' }),
    ).not.toBeInTheDocument();
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

  it('folds an ended turn to the final assistant output', async () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'prompt',
          items: [],
          attachments: [],
        },
        {
          id: 'm-middle',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-middle', text: 'middle step' }],
          attachments: [],
        },
        {
          id: 'm-final',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-final', text: 'final answer' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          durationMs: 123000,
          runID: 'r-1',
        },
      ],
    );
    render(<ChatView />);

    const worked = screen.getByRole('button', {
      name: 'Worked for 2m 3s',
    });
    const final = screen.getByText('final answer');
    expect(
      worked.compareDocumentPosition(final) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.queryByText('middle step')).not.toBeInTheDocument();
    await userEvent.setup().click(worked);
    expect(screen.getByText('middle step')).toBeInTheDocument();
    expect(screen.getByText('final answer')).toBeInTheDocument();
  });

  it('keeps the worked header for legacy turns without duration', async () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'prompt',
          items: [],
          attachments: [],
        },
        {
          id: 'm-middle',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-middle', text: 'middle step' }],
          attachments: [],
        },
        {
          id: 'm-final',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-final', text: 'final answer' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          runID: 'r-1',
        },
      ],
    );
    render(<ChatView />);

    const header = screen.getByRole('button', { name: 'Worked for …' });
    expect(screen.queryByText('middle step')).not.toBeInTheDocument();
    await userEvent.setup().click(header);
    expect(screen.getByText('middle step')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /Process \(/ }),
    ).not.toBeInTheDocument();
  });

  it('keeps a running turn expanded and shows Working at the top', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'prompt',
          items: [],
          attachments: [],
        },
        {
          id: 'm-live',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-live', text: 'streamed answer' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          runID: 'r-1',
        },
      ],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    expect(screen.getByText('Working…')).toBeInTheDocument();
    expect(screen.getByText('streamed answer')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /Process \(/ }),
    ).not.toBeInTheDocument();
  });

  it('renders message-peek ticks with user/answer previews', async () => {
    vi.useFakeTimers();
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
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });

    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('build search');
    expect(tooltip).toHaveTextContent('search built');
    vi.useRealTimers();
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

  it('renders a new-chat draft composer when no session is open', () => {
    stateRoot.resetWorkspace();
    render(<ChatView />);
    expect(screen.getByText('What shall we craft today?')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Choose workspace' }),
    ).toBeInTheDocument();
    expect(screen.getByText('Workspace mode')).toBeInTheDocument();
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

describe('ChatView draft session defaults', () => {
  it('follows changed defaults until the user picks a mode manually', async () => {
    const user = userEvent.setup();
    stateRoot.resetWorkspace();
    useStore.setState({
      configured: true,
      workspace: '/tmp/w',
      conversations: {},
      sessionDefaults: { mode: 'workspace', think: 'medium' },
    });
    render(<ChatView />);
    expect(
      screen.getByRole('button', { name: 'Workspace mode' }),
    ).toBeInTheDocument();

    // Defaults changed in Settings while the same draft stays open:
    // the pre-send pill must follow when untouched.
    act(() => {
      useStore.setState({
        sessionDefaults: { mode: 'read-only', think: 'high' },
      });
    });
    expect(
      screen.getByRole('button', { name: 'Read-only' }),
    ).toBeInTheDocument();

    // A manual pick for this draft survives a later default change.
    await user.click(screen.getByRole('button', { name: 'Read-only' }));
    await user.click(screen.getByText('Workspace mode'));
    act(() => {
      useStore.setState({
        sessionDefaults: { mode: 'yolo', think: 'high' },
      });
    });
    expect(
      screen.getByRole('button', { name: 'Workspace mode' }),
    ).toBeInTheDocument();
  });
});
