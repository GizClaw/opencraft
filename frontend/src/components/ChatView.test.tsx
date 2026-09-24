import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { stateRoot } from '../state/app';
import type { MessageView, TurnArtifacts } from '../lib/store';
import type { SandboxProcess, SessionMeta } from '../lib/types';
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
  steerTurn: vi.fn(),
  cancelTurn: vi.fn(async () => undefined),
  processes: vi.fn(async () => [] as SandboxProcess[]),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

function manyMessages(n: number): MessageView[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `m-${i}`,
    role: 'user',
    text: `message-${i}`,
    items: [],
    attachments: [],
  }));
}

// assistantTurn is a running turn's transcript so far: the ask that
// opened it and the answer the user interrupts mid-flight.
function assistantTurn(): MessageView[] {
  return [
    {
      id: 'm-ask',
      role: 'user',
      text: 'start the refactor',
      items: [],
      attachments: [],
    },
    {
      id: 'm-answer',
      role: 'assistant',
      text: 'working on it',
      // Assistant text lives in items: the row renders the ordered
      // blocks, and a message with no blocks folds away to nothing.
      items: [{ kind: 'text', id: 'i-answer', text: 'working on it' }],
      attachments: [],
    },
  ];
}

// thoughtTranscript is a settled turn whose answer streamed a reasoning
// block: the one thing the activity card holds on to by itself.
function thoughtTranscript(id: string, thought: string): MessageView[] {
  return [
    {
      id: `${id}-ask`,
      role: 'user',
      text: 'go',
      items: [],
      attachments: [],
    },
    {
      id: `${id}-answer`,
      role: 'assistant',
      text: '',
      items: [
        { kind: 'text', id: `${id}-text`, text: 'working on it' },
        { kind: 'reasoning', id: `${id}-think`, text: thought },
      ],
      attachments: [],
    },
  ];
}

function setConversation(
  messages: MessageView[],
  turnArtifacts: TurnArtifacts[] = [],
  mode = 'workspace',
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
        mode,
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
  apiMock.steerTurn.mockResolvedValue(undefined);
  // clearAllMocks keeps implementations, so the feed's default answer is
  // restored here rather than left over from the test before.
  apiMock.processes.mockResolvedValue([]);
});

// stubTranscriptGeometry answers the layout questions a windowed transcript
// asks and jsdom cannot: how tall the viewport is (600px), how tall a block
// is once mounted (120px), and where the scroller is (a scroll position the
// test can move; the bottom until it does, which is where a session opens).
// The stubs sit on the prototypes because the list reads them while the
// transcript is being committed — before a test can hold the element. Fair
// warning: every element answers with the same 800x600 box, so this is for
// tests that read the transcript, not for tests that measure anything.
function stubTranscriptGeometry(): () => void {
  const keys = [
    'getBoundingClientRect',
    'clientHeight',
    'scrollHeight',
    'scrollTop',
    'offsetHeight',
  ] as const;
  const saved = new Map<
    (typeof keys)[number],
    PropertyDescriptor | undefined
  >();
  for (const key of keys) {
    saved.set(key, Object.getOwnPropertyDescriptor(HTMLElement.prototype, key));
  }
  const offsets = new WeakMap<HTMLElement, number>();
  // listHeight is what the transcript's own container reserves: the
  // scroller's scrollHeight follows it, so a pin to the bottom lands where
  // the app means it to.
  const listHeight = function (this: HTMLElement) {
    const list = this.querySelector?.('[data-transcript-list]');
    const height = Number.parseFloat(
      (list as HTMLElement | null)?.style.height ?? '',
    );
    return Number.isFinite(height) ? height : 0;
  };
  Object.defineProperties(HTMLElement.prototype, {
    getBoundingClientRect: {
      configurable: true,
      value: () => ({
        top: 0,
        left: 0,
        right: 800,
        bottom: 600,
        width: 800,
        height: 600,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      }),
    },
    clientHeight: { configurable: true, get: () => 600 },
    offsetHeight: { configurable: true, get: () => 120 },
    scrollHeight: {
      configurable: true,
      get(this: HTMLElement) {
        return Math.max(this.clientHeight, listHeight.call(this));
      },
    },
    scrollTop: {
      configurable: true,
      get(this: HTMLElement) {
        return offsets.get(this) ?? this.scrollHeight;
      },
      set(this: HTMLElement, value: number) {
        offsets.set(this, value);
      },
    },
  });
  return () => {
    for (const key of keys) {
      const descriptor = saved.get(key);
      if (descriptor) {
        Object.defineProperty(HTMLElement.prototype, key, descriptor);
      } else {
        delete (HTMLElement.prototype as unknown as Record<string, unknown>)[
          key
        ];
      }
    }
  };
}

// Every test that stubs the geometry gets it taken back afterwards, so a
// failing assertion cannot leave the next test laying out in 600px.
let restoreTranscriptGeometry: (() => void) | null = null;
afterEach(() => {
  restoreTranscriptGeometry?.();
  restoreTranscriptGeometry = null;
});

describe('ChatView transcript windowing', () => {
  it('mounts a screenful and loads earlier at the top', async () => {
    restoreTranscriptGeometry = stubTranscriptGeometry();
    setConversation(manyMessages(250));
    render(<ChatView />);

    const scroller = screen.getByTestId('chat-scroll');
    const mounted = () =>
      Array.from(scroller.querySelectorAll('[data-msg-index]')).map((el) =>
        Number(el.getAttribute('data-msg-index')),
      );
    // A session opens at its newest row: that row is mounted, the ones just
    // above it are, and the rest of the 200 the render window holds are not.
    expect(within(scroller).getByText('message-249')).toBeInTheDocument();
    expect(mounted().length).toBeLessThan(20);
    expect(Math.min(...mounted())).toBeGreaterThan(200);
    expect(within(scroller).queryByText('message-0')).not.toBeInTheDocument();

    // Reading to the top asks for the window above the one that is loaded.
    // The rows land *above* the reader — the viewport is anchored to the
    // content that was on screen — so the newest row, thousands of pixels
    // down by now, leaves the DOM.
    scroller.scrollTop = 0;
    fireEvent.scroll(scroller);
    await waitFor(() => expect(scroller.scrollTop).toBeGreaterThan(0));
    expect(within(scroller).queryByText('message-249')).not.toBeInTheDocument();

    // The whole session is loaded now. Asking for the top again mounts its
    // first row — and the DOM is still a screenful, not 250 rows.
    scroller.scrollTop = 0;
    fireEvent.scroll(scroller);
    expect(await within(scroller).findByText('message-0')).toBeInTheDocument();
    expect(mounted().length).toBeLessThan(20);
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

  it('shows a staged queue draft and cancelling it restores the draft', async () => {
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

    const banner = screen.getByTestId('chat-staged-banner');
    expect(within(banner).getByText(/staged question/)).toBeInTheDocument();
    fireEvent.click(
      within(banner).getByRole('button', {
        name: 'Cancel queued message',
      }),
    );
    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
    expect(screen.queryByTestId('chat-staged-banner')).not.toBeInTheDocument();
    // The X returns the draft to the composer instead of discarding it.
    await waitFor(() =>
      expect(screen.getByRole('textbox')).toHaveTextContent('staged question'),
    );
  });

  it('force-cancels the superseded run while a barge-in waits', async () => {
    setConversation([]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    // A barge-in Enter leaves the conversation starting while the
    // backend interrupts r-old; the banner must offer a force-cancel.
    actor?.send({ type: 'SEND_STARTED' });
    expect(actor?.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
    });

    render(<ChatView />);

    const banner = screen.getByTestId('chat-staged-banner');
    expect(
      within(banner).getByText(/Interrupting the current reply/i),
    ).toBeInTheDocument();
    fireEvent.click(within(banner).getByRole('button', { name: 'Stop' }));

    expect(apiMock.cancelTurn).toHaveBeenCalledWith('r-old');
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
      '/tmp/w',
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
    expect(screen.getByText(/Enter steers the reply/i)).toBeInTheDocument();

    // Tab stages the draft and clears the composer, which hides the
    // hint and surfaces the queue banner instead.
    await userEvent.keyboard('{Tab}');
    expect(
      screen.queryByText(/Enter steers the reply/i),
    ).not.toBeInTheDocument();
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'next question',
      interrupt: false,
    });
  });

  it('keeps Stop reachable while a draft steers with Send', async () => {
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

    // Typing a draft restores Send, and Stop stays beside it: stopping
    // the reply must not require emptying the composer first.
    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('next question');
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();

    // Stop cancels the run and leaves the draft alone.
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(apiMock.cancelTurn).toHaveBeenCalledWith('r-old');
    expect(screen.getByRole('textbox')).toHaveTextContent('next question');

    // Send steers like Enter; the draft becomes an optimistic row, so
    // the composer empties and Stop is primary again.
    await userEvent.setup().click(screen.getByRole('button', { name: 'Send' }));
    await vi.waitFor(() => expect(apiMock.steerTurn).toHaveBeenCalled());
    expect(apiMock.steerTurn).toHaveBeenCalledWith('r-old', 'next question');
    expect(apiMock.startTurn).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Send' }),
    ).not.toBeInTheDocument();
  });

  it('stages the draft when Enter arrives before the run has an id', async () => {
    setConversation([], []);
    stateRoot.registry.get('s-1')?.send({ type: 'SEND_STARTED' });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('waits for the run');
    await userEvent.keyboard('{Enter}');

    // No run id exists yet, so nothing can take a steer: the draft is
    // staged exactly like a Tab queue instead of being dropped.
    expect(apiMock.steerTurn).not.toHaveBeenCalled();
    expect(apiMock.startTurn).not.toHaveBeenCalled();
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'waits for the run',
      interrupt: false,
    });
    expect(screen.getByRole('textbox')).not.toHaveTextContent(
      'waits for the run',
    );
  });

  it('refuses Enter with an attachment and keeps the draft', async () => {
    setConversation([], []);
    apiMock.pickFile.mockResolvedValue('/tmp/notes.txt');
    apiMock.readAttachment.mockResolvedValue({
      path: '/tmp/notes.txt',
      name: 'notes.txt',
      media_type: 'text/plain',
      size: 12,
    });
    render(<ChatView />);
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Attach files' }));
    await waitFor(() =>
      expect(screen.getByText('notes.txt')).toBeInTheDocument(),
    );

    // The turn starts with the attachment still staged.
    stateRoot.registry.get('s-1')?.send({ type: 'SEND_STARTED' });
    stateRoot.registry
      .get('s-1')
      ?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('with file');
    await userEvent.keyboard('{Enter}');

    // Steer carries text only: the refusal says so and nothing moves —
    // the text and the attachment both stay in the composer.
    expect(apiMock.steerTurn).not.toHaveBeenCalled();
    expect(apiMock.startTurn).not.toHaveBeenCalled();
    // Toasts render in their own host; the store is the assertion point.
    const toasts = useStore.getState().toasts;
    expect(toasts).toHaveLength(1);
    expect(toasts[0].kind).toBe('warning');
    expect(toasts[0].text).toMatch(/Steering carries text only/i);
    expect(screen.getByText('notes.txt')).toBeInTheDocument();
    expect(screen.getByRole('textbox')).toHaveTextContent('with file');
    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
  });

  it('Cmd/Ctrl+Enter sends the draft as a barge-in', async () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-next',
      context_id: 's-1',
    });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('do this instead');
    await userEvent.keyboard('{Meta>}{Enter}{/Meta}');

    // The draft was sent as the replacement turn (interrupting r-old),
    // not steered into it.
    await vi.waitFor(() => expect(apiMock.startTurn).toHaveBeenCalled());
    expect(apiMock.steerTurn).not.toHaveBeenCalled();
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({
        content: {
          parts: [
            expect.objectContaining({
              type: 'text',
              text: 'do this instead',
            }),
          ],
        },
      }),
      '/tmp/w',
    );
    expect(screen.getByRole('textbox')).not.toHaveTextContent(
      'do this instead',
    );
  });

  it('renders a mid-turn interjection as a note, not as a new turn', async () => {
    // A running turn with an answer already streamed in: the note has to
    // land between the ask and the continuation, in the position the
    // user typed it, rather than reading as a second user turn.
    setConversation(assistantTurn(), [
      { id: 't-1', start: 0, docs: [], runID: 'r-old' },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    render(<ChatView />);

    // Mid-turn typing steers instead of interrupting.
    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('also check the docs');
    await userEvent.keyboard('{Enter}');
    await vi.waitFor(() =>
      expect(apiMock.steerTurn).toHaveBeenCalledWith(
        'r-old',
        'also check the docs',
      ),
    );
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    // The row is drawn as an interjection from the first paint: it says
    // that it is waiting for the next step and it never wears the
    // accent bubble the transcript wraps around a turn the user opened.
    const note = await screen.findByTestId('steer-note');
    expect(note).toHaveAttribute('data-steer-state', 'pending');
    expect(
      within(note).getByText(/waiting for the next step/i),
    ).toBeInTheDocument();
    expect(within(note).getByText('also check the docs')).toBeInTheDocument();
    // The ask is the only bubble in the transcript: the interjection
    // never wears the accent fill that means "I started this turn".
    expect(note.querySelector('.user-bubble-md')).toBeNull();
    expect(document.querySelectorAll('.user-bubble-md')).toHaveLength(1);

    // Position is the transcript's own order: ask, then the answer so
    // far, then the interjection.
    const scroller = screen.getByTestId('chat-scroll');
    const order = Array.from(scroller.querySelectorAll('[data-msg-index]')).map(
      (el) => el.getAttribute('data-msg-index'),
    );
    expect(order).toEqual(['0', '1', '2']);
    expect(within(scroller).getByText('working on it')).toBeInTheDocument();
  });

  it('stamps the note delivered when the run picked it up', async () => {
    setConversation(assistantTurn(), [
      { id: 't-1', start: 0, docs: [], runID: 'r-old' },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('also check the docs');
    await userEvent.keyboard('{Enter}');
    await screen.findByTestId('steer-note');

    // steer_pending 0 is a real zero: a boundary read the queue, so the
    // note settles into its plain state and offers no actions.
    await act(async () => {
      useStore.getState().handleEvent({
        type: 'turn_end',
        data: {
          run_id: 'r-old',
          conversation_id: 's-1',
          status: 'completed',
          steer_pending: 0,
        },
      });
    });
    const note = await screen.findByTestId('steer-note');
    expect(note).toHaveAttribute('data-steer-state', 'delivered');
    expect(
      within(note).queryByRole('button', { name: 'Send as a new turn' }),
    ).not.toBeInTheDocument();
    expect(within(note).getByText('also check the docs')).toBeInTheDocument();
  });

  it('keeps an undelivered interjection in place with its actions', async () => {
    setConversation(assistantTurn(), [
      { id: 't-1', start: 0, docs: [], runID: 'r-old' },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-next',
      context_id: 's-1',
    });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('changed my mind');
    await userEvent.keyboard('{Enter}');
    await screen.findByTestId('steer-note');

    // The turn ends without reaching the boundary a steer is delivered
    // at. The text never entered the conversation, so the note stays —
    // in its transcript position, marked, and with the actions that
    // keep the text from being lost.
    await act(async () => {
      useStore.getState().handleEvent({
        type: 'turn_end',
        data: {
          run_id: 'r-old',
          conversation_id: 's-1',
          status: 'completed',
          steer_pending: 1,
        },
      });
    });
    const note = await screen.findByTestId('steer-note');
    expect(note).toHaveAttribute('data-steer-state', 'undelivered');
    expect(within(note).getByText('changed my mind')).toBeInTheDocument();
    const scroller = screen.getByTestId('chat-scroll');
    // It sits after the answer it missed, which is where the user saw
    // it land — not stacked among the turn's other rows.
    const order = Array.from(scroller.querySelectorAll('[data-msg-index]')).map(
      (el) => el.getAttribute('data-msg-index'),
    );
    expect(order).toEqual(['0', '1', '2']);

    // Copying is not a decision: the text can leave the note without
    // committing to another turn, and the note stays put.
    // The stub goes in after setup(): userEvent.setup() installs its own
    // clipboard mock on navigator and would otherwise swallow the call.
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    await user.click(within(note).getByRole('button', { name: 'Copy text' }));
    await vi.waitFor(() =>
      expect(
        within(note).getByRole('button', { name: 'Copied' }),
      ).toBeInTheDocument(),
    );
    expect(writeText).toHaveBeenCalledWith('changed my mind');
    expect(screen.getByTestId('steer-note')).toBeInTheDocument();

    // Resending sends the text as a turn of its own and drops the note.
    await userEvent
      .setup()
      .click(within(note).getByRole('button', { name: 'Send as a new turn' }));
    await vi.waitFor(() => expect(apiMock.startTurn).toHaveBeenCalled());
    expect(screen.queryByTestId('steer-note')).not.toBeInTheDocument();
  });

  it('drops an undelivered interjection without sending it', async () => {
    setConversation(assistantTurn(), [
      { id: 't-1', start: 0, docs: [], runID: 'r-old' },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    render(<ChatView />);

    await userEvent.setup().click(screen.getByRole('textbox'));
    await userEvent.keyboard('never mind');
    await userEvent.keyboard('{Enter}');
    await screen.findByTestId('steer-note');
    await act(async () => {
      useStore.getState().handleEvent({
        type: 'turn_end',
        data: {
          run_id: 'r-old',
          conversation_id: 's-1',
          status: 'completed',
          steer_pending: null,
        },
      });
    });

    // null means the backend could not read the count, so the row is
    // kept as undelivered rather than claimed delivered.
    const note = await screen.findByTestId('steer-note');
    expect(note).toHaveAttribute('data-steer-state', 'undelivered');
    await userEvent
      .setup()
      .click(within(note).getByRole('button', { name: 'Discard' }));
    expect(screen.queryByTestId('steer-note')).not.toBeInTheDocument();
    expect(apiMock.startTurn).not.toHaveBeenCalled();
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
    // The bubble is the only element carrying its own bottom-right
    // corner radius; `.rounded-card` alone matches a dozen containers.
    const text = document.querySelector('.rounded-br-tight p');
    expect(text).not.toBeNull();
    const bubble = text!.closest('.rounded-br-tight');
    expect(bubble).not.toBeNull();

    // Images render above the bubble, the file chip below it, and
    // neither keeps a bubble background behind it.
    const image = screen.getByRole('img', { name: 'pic.png' });
    expect(image.closest('.rounded-br-tight')).toBeNull();
    expect(
      image.compareDocumentPosition(text!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    const fileChip = screen.getByRole('button', { name: '5 files' });
    expect(fileChip.closest('.rounded-br-tight')).toBeNull();
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

  // A fold rewrites the conversation prefix, which invalidates the
  // provider's prompt cache: the note is the only place the user can learn
  // why the next request cost more, and why the session is under pressure
  // once folding stops working.
  it('explains a folded turn and a strained compaction', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'long task',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'still going' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          compaction: { folds: 2 },
        },
      ],
    );
    const view = render(<ChatView />);
    expect(screen.getByText('Context compacted (2 fold)')).toBeInTheDocument();
    expect(
      screen.getByText(/billed at full input price once/),
    ).toBeInTheDocument();
    view.unmount();

    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'long task',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 't-1', text: 'still going' }],
          attachments: [],
        },
      ],
      [
        {
          id: 'turn-1',
          start: 0,
          docs: [],
          compaction: { folds: 0, failures: 3, notified: true },
        },
      ],
    );
    render(<ChatView />);
    expect(
      screen.getByText('Context compaction is out of options'),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/3 failed attempt\(s\) standing/),
    ).toBeInTheDocument();
  });

  it('shows no compaction note for a turn that did not fold', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'short task',
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
          compaction: { folds: 0 },
        },
      ],
    );
    render(<ChatView />);
    expect(screen.queryByText(/Context compacted/)).not.toBeInTheDocument();
    expect(
      screen.queryByText(/compaction is out of options/),
    ).not.toBeInTheDocument();
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

  it('windows a long process list instead of mounting every row', async () => {
    const messages: MessageView[] = [
      {
        id: 'm-user',
        role: 'user',
        text: 'prompt',
        items: [],
        attachments: [],
      },
    ];
    for (let i = 0; i < 120; i += 1) {
      messages.push({
        id: `m-${i}`,
        role: 'assistant',
        text: '',
        items: [
          {
            kind: 'tool_call',
            id: `part-${i}`,
            tool: {
              id: `call-${i}`,
              name: 'exec_command',
              args: '{}',
              status: 'done',
            },
          },
        ],
        attachments: [],
      });
    }
    messages.push({
      id: 'm-final',
      role: 'assistant',
      text: '',
      items: [{ kind: 'text', id: 't-final', text: 'final answer' }],
      attachments: [],
    });
    setConversation(messages, [
      { id: 'turn-1', start: 0, docs: [], durationMs: 123000, runID: 'r-1' },
    ]);
    render(<ChatView />);

    // The virtualizer reads the scroller's geometry; jsdom has none.
    const scroller = screen.getByTestId('chat-scroll');
    Object.defineProperty(scroller, 'clientHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(scroller, 'scrollHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(scroller, 'scrollTop', {
      configurable: true,
      value: 0,
      writable: true,
    });
    scroller.getBoundingClientRect = () =>
      ({
        top: 0,
        left: 0,
        right: 800,
        bottom: 600,
        width: 800,
        height: 600,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      }) as DOMRect;

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Worked for 2m 3s' }));

    // jsdom has no layout, so the virtualizer cannot compute *which*
    // rows are visible; what it can prove is that the list went virtual:
    // the container reserves the estimated height of all 120 rows, and
    // the rows themselves are not mounted eagerly. The real-browser
    // behaviour is covered by the perf e2e spec.
    const container = scroller.querySelector<HTMLElement>(
      'div[style*="position: relative"]',
    );
    expect(container).not.toBeNull();
    const height = Number.parseInt(container?.style.height ?? '0', 10);
    expect(height).toBeGreaterThan(120 * 60);
    expect(scroller.querySelectorAll('[data-index]').length).toBeLessThan(120);
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

// A turn the engine never archived comes back on the next assembly as an
// interrupted one (host/recover.go), and the notice that renders it is
// the only place the user can pick the work up again: the frontier is
// never replayed, so "continue" re-sends the turn's own message with the
// partial reply already in context.
describe('ChatView interrupted turn recovery', () => {
  const restartCopy = 'The app closed or crashed while this reply was running.';

  it('offers Continue on the newest interrupted turn and re-sends its message', async () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'build the search index',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 'p-1', text: 'wrote half of it' }],
          attachments: [],
        },
      ],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'interrupted',
          interruptCause: 'app_restart',
        },
      ],
    );
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-2',
      context_id: 's-1',
    });
    render(<ChatView />);

    // The recovery cause has its own copy instead of the generic
    // "interrupted before it finished" line.
    expect(screen.getByText('Reply interrupted')).toBeInTheDocument();
    expect(screen.getByText(restartCopy)).toBeInTheDocument();

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Continue' }));

    await vi.waitFor(() => expect(apiMock.startTurn).toHaveBeenCalled());
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({
        content: {
          parts: [
            expect.objectContaining({
              type: 'text',
              text: 'build the search index',
            }),
          ],
        },
      }),
      '/tmp/w',
    );
  });

  it('keeps an older interrupted turn static', () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'first ask',
          items: [],
          attachments: [],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 'p-1', text: 'partial' }],
          attachments: [],
        },
        {
          id: 'm-3',
          role: 'user',
          text: 'second ask',
          items: [],
          attachments: [],
        },
        {
          id: 'm-4',
          role: 'assistant',
          text: '',
          items: [{ kind: 'text', id: 'p-2', text: 'done' }],
          attachments: [],
        },
      ],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'interrupted',
          interruptCause: 'app_restart',
        },
        { id: 't-2', start: 2, docs: [], status: 'completed' },
      ],
    );
    render(<ChatView />);

    // The notice explains the older turn, but it has been answered over
    // since: continuing it would only repeat history.
    expect(screen.getByText('Reply interrupted')).toBeInTheDocument();
    expect(screen.getByText(restartCopy)).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Continue' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Edit & resend' }),
    ).not.toBeInTheDocument();
  });

  it('restores the original message into the composer for editing', async () => {
    setConversation(
      [
        {
          id: 'm-1',
          role: 'user',
          text: 'summarize notes.txt',
          items: [],
          attachments: [
            {
              id: 'a-1',
              kind: 'file',
              path: '/tmp/notes.txt',
              name: 'notes.txt',
            },
          ],
        },
        {
          id: 'm-2',
          role: 'assistant',
          text: '',
          items: [],
          attachments: [],
        },
      ],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'interrupted',
          interruptCause: 'app_restart',
        },
      ],
    );
    render(<ChatView />);

    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Edit & resend' }));

    // Both halves of the message are back in the composer so the user
    // edits what they originally sent, not a text-only copy of it.
    expect(screen.getByRole('textbox')).toHaveTextContent(
      'summarize notes.txt',
    );
    expect(screen.getByLabelText('Remove attachment')).toBeInTheDocument();
    expect(apiMock.startTurn).not.toHaveBeenCalled();
  });

  it('offers Continue from a windowed transcript', async () => {
    restoreTranscriptGeometry = stubTranscriptGeometry();
    const messages: MessageView[] = [
      {
        id: 'm-0',
        role: 'user',
        text: 'original ask',
        items: [],
        attachments: [],
      },
      ...Array.from({ length: 249 }, (_, i) => ({
        id: `m-${i + 1}`,
        role: 'assistant' as const,
        text: '',
        items: [{ kind: 'text' as const, id: `p-${i}`, text: `step ${i}` }],
        attachments: [],
      })),
    ];
    setConversation(messages, [
      {
        id: 't-1',
        start: 0,
        docs: [],
        status: 'interrupted',
        interruptCause: 'app_restart',
      },
    ]);
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-2',
      context_id: 's-1',
    });
    render(<ChatView />);

    // The windowed renderer draws the same notice; the turn's user
    // message is outside the window and is still the one that is sent.
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Continue' }));

    await vi.waitFor(() => expect(apiMock.startTurn).toHaveBeenCalled());
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({
        content: {
          parts: [
            expect.objectContaining({ type: 'text', text: 'original ask' }),
          ],
        },
      }),
      '/tmp/w',
    );
  });
});

describe('ChatView turn end notice diagnostics', () => {
  it('shows the correlation id and raw detail for non-user failures', () => {
    setConversation(
      [{ id: 'm-1', role: 'user', text: 'hello', items: [], attachments: [] }],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'failed',
          error: 'provider_failure during generate',
          requestID: 'req-xyz',
          responseID: 'resp-xyz',
        },
      ],
    );
    render(<ChatView />);

    expect(screen.getByText('Last reply failed')).toBeInTheDocument();
    expect(screen.getByText(/Request ID: req-xyz/)).toBeInTheDocument();
    expect(screen.getByText(/Response ID: resp-xyz/)).toBeInTheDocument();
    // The friendly summary stays the primary detail; the raw engine
    // reason renders beneath it in small text.
    expect(
      screen.getByText('provider_failure during generate'),
    ).toBeInTheDocument();
  });

  it('keeps provider diagnostics out of user-stopped notices', () => {
    setConversation(
      [{ id: 'm-1', role: 'user', text: 'hello', items: [], attachments: [] }],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'canceled',
          error: 'context canceled',
          requestID: 'req-xyz',
          responseID: 'resp-xyz',
        },
      ],
    );
    render(<ChatView />);

    expect(screen.getByText('Reply cancelled')).toBeInTheDocument();
    expect(screen.queryByText(/req-xyz/)).not.toBeInTheDocument();
    expect(screen.queryByText(/resp-xyz/)).not.toBeInTheDocument();
    expect(screen.queryByText('context canceled')).not.toBeInTheDocument();
  });

  it('reads a deadline as a timeout, not as a user stop', () => {
    setConversation(
      [{ id: 'm-1', role: 'user', text: 'hello', items: [], attachments: [] }],
      [
        {
          id: 't-1',
          start: 0,
          docs: [],
          status: 'canceled',
          error: 'context deadline exceeded',
          errorKind: 'timeout',
          requestID: 'req-xyz',
          responseID: 'resp-xyz',
        },
      ],
    );
    render(<ChatView />);

    // Same canceled status as the user stop above, different ending:
    // the notice says so, keeps the raw reason (and the ids) and lets
    // the turn be carried on.
    expect(screen.getByText('Reply timed out')).toBeInTheDocument();
    expect(
      screen.getByText(
        'This turn timed out before it finished. Try again, or split the work into smaller steps.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('context deadline exceeded')).toBeInTheDocument();
    expect(screen.getByText(/Request ID: req-xyz/)).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Continue' }),
    ).toBeInTheDocument();
    expect(screen.queryByText('Reply cancelled')).not.toBeInTheDocument();
  });
});

describe('ChatView jump-to-latest pill', () => {
  it('appears after scrolling away from the bottom and snaps back on click', () => {
    setConversation(manyMessages(40));
    render(<ChatView />);

    const scroller = screen.getByTestId('chat-scroll');
    Object.defineProperty(scroller, 'scrollHeight', {
      configurable: true,
      value: 8000,
    });
    Object.defineProperty(scroller, 'clientHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(scroller, 'scrollTop', {
      configurable: true,
      writable: true,
      value: 0,
    });

    // Hidden while pinned to the newest output.
    expect(
      screen.queryByRole('button', { name: 'Jump to latest' }),
    ).not.toBeInTheDocument();

    // Reach the real bottom once, then scroll up to read history.
    scroller.scrollTop = 7400;
    fireEvent.scroll(scroller);
    scroller.scrollTop = 200;
    fireEvent.scroll(scroller);

    const jump = screen.getByRole('button', { name: 'Jump to latest' });
    fireEvent.click(jump);

    expect(scroller.scrollTop).toBe(8000);
    expect(
      screen.queryByRole('button', { name: 'Jump to latest' }),
    ).not.toBeInTheDocument();
  });

  // The heaviest test of the suite: it mounts two 250-message transcripts
  // (~2x the 200-row window) through the markdown renderer, which is ~2s
  // on a laptop and has crossed the default 5s budget on a loaded CI
  // runner. It asserts the same thing either way, so it gets the room the
  // rendering cost needs instead of a thinner fixture that would stop
  // proving the window sits at the newest row.
  it('re-pins the follow when another conversation is opened', () => {
    restoreTranscriptGeometry = stubTranscriptGeometry();
    setConversation(manyMessages(250));
    render(<ChatView />);

    const scroller = screen.getByTestId('chat-scroll');
    Object.defineProperty(scroller, 'scrollHeight', {
      configurable: true,
      value: 8000,
    });
    Object.defineProperty(scroller, 'clientHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(scroller, 'scrollTop', {
      configurable: true,
      writable: true,
      value: 0,
    });

    // Read the open conversation from its top: the view is unpinned and
    // offers the pill.
    scroller.scrollTop = 7400;
    fireEvent.scroll(scroller);
    scroller.scrollTop = 0;
    fireEvent.scroll(scroller);
    expect(
      screen.getByRole('button', { name: 'Jump to latest' }),
    ).toBeInTheDocument();

    // Open another conversation. The pin belongs to the transcript, not
    // to the view: the new session must start following its newest
    // message instead of inheriting "the reader scrolled away".
    act(() => {
      stateRoot.sendFocus({ type: 'OPEN_SESSION', id: 's-2' });
    });
    const request = stateRoot.focusSnapshot.context.request;
    act(() => {
      stateRoot.registry
        .ensure('s-2', { workspaceGeneration: stateRoot.generation() })
        ?.send({ type: 'NEW_CHAT_READY' });
      stateRoot.sendFocus({
        type: 'OPEN_SUCCEEDED',
        request,
        sessionID: 's-2',
      });
      useStore.setState({
        conversations: {
          's-2': {
            messages: manyMessages(250),
            turnArtifacts: [],
            mode: 'workspace',
            think: 'medium',
            model: '',
            pendingInteracts: [],
          },
        },
      });
    });

    expect(screen.getByText('message-249')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Jump to latest' }),
    ).not.toBeInTheDocument();
  }, 15000);
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
    expect(
      screen.getByRole('button', { name: 'Workspace mode' }),
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

describe('ChatView draft session defaults', () => {
  it('names the failed step of a group that has one', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'error',
                result: '{"exit_code":1,"stdout":"","stderr":"FAIL"}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'exec_command',
                args: '{"command":"go vet ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    const header = screen.getByRole('button', { name: /Ran 3 commands/ });
    expect(header).toHaveTextContent('1 step failed');
    // One failure names the step, so a collapsed burst still says where
    // to look.
    expect(header).toHaveTextContent('go test ./...');
    expect(header).not.toHaveTextContent('3/3');
    // The row reads left to right: the burst's count, the step it ended
    // on, and the failure report closing the row on the right.
    const step = within(header).getByTestId('tool-group-step');
    const failed = within(header).getByTestId('tool-group-failed');
    expect(step).toHaveTextContent('go vet ./...');
    expect(step.compareDocumentPosition(failed)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it('counts failures without naming a step when several failed', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'error',
                result: '{"exit_code":1,"stdout":"","stderr":"boom"}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'error',
                result: '{"exit_code":1,"stdout":"","stderr":"FAIL"}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'exec_command',
                args: '{"command":"go vet ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    const header = screen.getByRole('button', { name: /Ran 3 commands/ });
    expect(header).toHaveTextContent('2 steps failed');
    // The step line still holds the call the burst stopped on; naming
    // both failures as well would crowd the row without saying which
    // one to look at first, so the expanded cards carry them.
    expect(header).toHaveTextContent('go vet ./...');
    expect(header).not.toHaveTextContent('go build ./...');
    expect(header).not.toHaveTextContent('go test ./...');
  });

  it('counts a failed step as settled while the burst still runs', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'error',
                result: '{"exit_code":1,"stdout":"","stderr":"FAIL"}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'exec_command',
                args: '{"command":"go vet ./..."}',
                status: 'running',
                seenAt: Date.now(),
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    // The header carries both halves of the state at once: what failed
    // and what is still in flight.
    const header = screen.getByRole('button', { name: /Ran 3 commands/ });
    expect(header).toHaveTextContent('1 step failed');
    expect(header).toHaveTextContent('go test ./...');
    expect(header).toHaveTextContent('go vet ./...');
    expect(header).toHaveTextContent('2/3');
  });

  it('shows the running step and progress in a grouped tool burst', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"ok","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'exec_command',
                args: '{"command":"go vet ./..."}',
                status: 'running',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    expect(screen.getByText('Ran 3 commands')).toBeInTheDocument();
    expect(screen.getByText('go vet ./...')).toBeInTheDocument();
    expect(screen.getByText('2/3')).toBeInTheDocument();
  });

  it('keeps the last step on screen after the turn ends', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'read_file',
                args: '{"file_path":"src/a.go"}',
                status: 'done',
                result: 'package main',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'read_file',
                args: '{"file_path":"src/b.go"}',
                status: 'done',
                result: 'package main',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    expect(screen.getByText('Ran 2 tools')).toBeInTheDocument();
    // The burst is over but the answer is still streaming: the header
    // keeps the call that just ran instead of blanking between calls.
    expect(screen.getByText('src/b.go')).toBeInTheDocument();
    // Nothing is running, so there is no progress left to count.
    expect(screen.queryByText('2/2')).not.toBeInTheDocument();

    act(() => {
      stateRoot.registry
        .get('s-1')
        ?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    });
    // The turn ending does not blank the line: a collapsed burst keeps
    // naming the call it ran, so scrolling back through history still
    // says what happened without opening every group.
    expect(screen.getByText('src/b.go')).toBeInTheDocument();
    expect(screen.getByText('Ran 2 tools')).toBeInTheDocument();
  });

  it('keeps the last step of every burst, not just the newest', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'read_file',
                args: '{"file_path":"src/a.go"}',
                status: 'done',
                result: 'package main',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'read_file',
                args: '{"file_path":"src/b.go"}',
                status: 'done',
                result: 'package main',
              },
            },
            { kind: 'text', id: 't-1', text: 'First pass done.' },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'read_file',
                args: '{"file_path":"src/c.go"}',
                status: 'done',
                result: 'package main',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-4',
              tool: {
                id: 'call-4',
                name: 'read_file',
                args: '{"file_path":"src/d.go"}',
                status: 'done',
                result: 'package main',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    // Each burst keeps its own last call in its header: the one the
    // model is working through and the one above it, which is already
    // history.
    expect(screen.getByText('src/d.go')).toBeInTheDocument();
    expect(screen.getByText('src/b.go')).toBeInTheDocument();
  });

  it('keeps the last command in the header once the turn ends', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'run the checks',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"ok","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-3',
              tool: {
                id: 'call-3',
                name: 'exec_command',
                args: '{"command":"go vet ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);
    act(() => {
      stateRoot.registry
        .get('s-1')
        ?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    });

    const header = screen.getByRole('button', { name: /Ran 3 commands/ });
    expect(within(header).getByTestId('tool-group-step')).toHaveTextContent(
      'go vet ./...',
    );
    // Nothing is running, so no progress counter outlives the turn.
    expect(header).not.toHaveTextContent('3/3');
  });

  it('shows the last command of a turn replayed from the archive', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'run the checks',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: 'All green.',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"ok","stderr":""}',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    // No RUN_STARTED: this transcript came out of the archive, so it is
    // history from the first frame.
    render(<ChatView />);

    const header = screen.getByRole('button', { name: /Ran 2 commands/ });
    expect(within(header).getByTestId('tool-group-step')).toHaveTextContent(
      'go test ./...',
    );
  });

  it('does not name a lone failure twice when it is the last step', () => {
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'run the suite',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'exec_command',
                args: '{"command":"go build ./..."}',
                status: 'done',
                result: '{"exit_code":0,"stdout":"","stderr":""}',
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'exec_command',
                args: '{"command":"go test ./..."}',
                status: 'error',
                result: '{"exit_code":1,"stdout":"","stderr":"FAIL"}',
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    const header = screen.getByRole('button', { name: /Ran 2 commands/ });
    expect(within(header).getByTestId('tool-group-step')).toHaveTextContent(
      'go test ./...',
    );
    expect(within(header).getByTestId('tool-group-failed')).toHaveTextContent(
      '1 step failed',
    );
    // The step line already spells the command out; repeating it beside
    // the failure count would print the same command twice in one row.
    expect(within(header).getAllByText('go test ./...')).toHaveLength(1);
  });

  it('ticks the elapsed time of the step in flight', async () => {
    vi.useFakeTimers();
    setConversation(
      [
        {
          id: 'm-user',
          role: 'user',
          text: 'build it',
          items: [],
          attachments: [],
        },
        {
          id: 'm-tools',
          role: 'assistant',
          text: '',
          items: [
            {
              kind: 'tool_call',
              id: 'p-1',
              tool: {
                id: 'call-1',
                name: 'grep',
                args: '{"pattern":"buildMu"}',
                status: 'running',
                seenAt: Date.now(),
              },
            },
            {
              kind: 'tool_call',
              id: 'p-2',
              tool: {
                id: 'call-2',
                name: 'grep',
                args: '{"pattern":"engines"}',
                status: 'running',
                seenAt: Date.now(),
              },
            },
          ],
          attachments: [],
        },
      ],
      [{ id: 'turn-1', start: 0, docs: [], runID: 'r-1' }],
    );
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    // The newest running call is the step in flight.
    expect(screen.getByText('engines')).toBeInTheDocument();
    expect(screen.getByText('0/2')).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    // Scoped to the row: the header's run read-out reads the same clock,
    // so an unscoped query would match both.
    const group = screen.getByRole('button', { name: /Ran 2 tools/ });
    expect(within(group).getByText('1s')).toBeInTheDocument();
    vi.useRealTimers();
  });

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

// A delegation note is an archived turn the app wrote to the model. It
// renders as a card from the turn's fields — never as a user bubble,
// whose accent pill is the transcript's word for "I started this turn".
describe('ChatView delegation note', () => {
  const noteRow = (): MessageView => ({
    id: 'm-note',
    role: 'user',
    text: '[delegated worker "researcher" finished: succeeded]\n\n## findings\n\nthe report',
    items: [],
    attachments: [],
    kind: 'delegation_note',
    note: {
      target: 'researcher',
      status: 'succeeded',
      card_id: 'card-1',
      run_id: 'run-child',
      parent_run_id: 'run-parent',
      body: '## findings\n\nthe report',
    },
  });

  it('renders the archived fields as a card instead of a bubble', () => {
    setConversation([noteRow()]);
    render(<ChatView />);

    const card = screen.getByTestId('delegation-note');
    expect(card).toBeInTheDocument();
    expect(card.dataset.status).toBe('succeeded');
    expect(document.querySelector('.user-bubble')).toBeNull();
    // The header names the subagent and the outcome; the body renders
    // the report as markdown, not as the bracketed line the model got.
    const scope = within(card);
    expect(scope.getByText('Delegated result')).toBeInTheDocument();
    expect(scope.getByText('researcher')).toBeInTheDocument();
    expect(scope.getByText('Succeeded')).toBeInTheDocument();
    expect(
      scope.getByRole('heading', { name: 'findings' }),
    ).toBeInTheDocument();
    expect(card.textContent).not.toContain('[delegated worker');
    // The reference line names the delegation for anyone who wants to
    // look it up again.
    expect(card.textContent).toContain('card card-1');
    expect(card.textContent).toContain('asked by run run-parent');
  });

  it('collapses and expands', async () => {
    setConversation([noteRow()]);
    render(<ChatView />);

    const card = screen.getByTestId('delegation-note');
    await userEvent.click(
      within(card).getByRole('button', { name: 'Hide the report' }),
    );
    expect(card.textContent).not.toContain('the report');
    await userEvent.click(
      within(card).getByRole('button', { name: 'Show the report' }),
    );
    await waitFor(() => expect(card.textContent).toContain('the report'));
  });

  // A note whose fields did not decode (a kind from a newer build, or a
  // payload the store could not read) still renders as the app's card,
  // from its own text: dressing it as the user speaking would be wrong
  // in a way the missing fields are not.
  it('falls back to the note text when the fields did not decode', () => {
    setConversation([
      {
        id: 'm-note-broken',
        role: 'user',
        text: '[delegated worker "researcher" finished: succeeded]\n\nraw text',
        items: [],
        attachments: [],
        kind: 'delegation_note',
      },
      {
        id: 'm-ask',
        role: 'user',
        text: 'the real question',
        items: [],
        attachments: [],
      },
    ]);
    render(<ChatView />);

    const card = screen.getByTestId('delegation-note');
    expect(card.textContent).toContain('raw text');
    expect(card.getAttribute('data-status')).toBeNull();
    expect(document.querySelector('.user-bubble')).not.toBeNull();
    expect(
      within(document.querySelector('.user-bubble') as HTMLElement).getByText(
        /the real question/,
      ),
    ).toBeInTheDocument();
  });
});

describe('ChatView user bubbles', () => {
  it('renders the sent text as markdown inside the bubble', () => {
    setConversation([
      {
        id: 'm-1',
        role: 'user',
        text: [
          '## Plan',
          '',
          '- first step',
          '- second step',
          '',
          '**bold** and `code`',
          '',
          '```ts',
          'const x = 1;',
          '```',
          '',
          'line one',
          'line two',
        ].join('\n'),
        items: [],
        attachments: [],
      },
    ]);
    render(<ChatView />);

    const bubble = document.querySelector('.user-bubble-md') as HTMLElement;
    expect(bubble).not.toBeNull();
    const scope = within(bubble);
    expect(scope.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    expect(scope.getAllByRole('listitem')).toHaveLength(2);
    expect(scope.getByText('bold').tagName).toBe('STRONG');
    expect(scope.getByText('code').tagName).toBe('CODE');
    // The fence renders highlighted tokens, so match on the bubble text.
    expect(bubble.textContent).toContain('const x = 1;');
    // A soft break inside one paragraph keeps its own line: the bubble
    // styles preserve the newlines the composer recorded.
    expect(
      Array.from(bubble.querySelectorAll('p')).some((p) =>
        p.textContent?.includes('line one\nline two'),
      ),
    ).toBe(true);
  });
});

// A turn's text arrives in blocks, and a block ends the moment a tool
// call (or the next block) starts. Only the block the model is still
// writing is unfinished text; the ones behind it are settled markdown
// and used to sit there raw (`## Plan` on screen, heading font nowhere)
// until the whole turn ended.
describe('ChatView streamed markdown', () => {
  function streamingTurn(): MessageView[] {
    return [
      {
        id: 'm-user',
        role: 'user',
        text: 'go',
        items: [],
        attachments: [],
      },
      {
        id: 'm-answer',
        role: 'assistant',
        text: '',
        items: [
          { kind: 'text', id: 'i-1', text: '## Plan\n\n- first step' },
          {
            kind: 'tool_call',
            id: 'p-1',
            tool: {
              id: 'call-1',
              name: 'exec_command',
              args: '{"command":"ls"}',
              status: 'done',
              result: '{"exit_code":0,"stdout":"","stderr":""}',
            },
          },
          { kind: 'text', id: 'i-2', text: '## Still writing' },
        ],
        attachments: [],
      },
    ];
  }

  it('parses a settled block while the turn is still running', () => {
    setConversation(streamingTurn(), [
      { id: 'turn-1', start: 0, docs: [], runID: 'r-1' },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    // The tool call ended this block: it is no longer growing, so its
    // markdown is what the reader sees.
    expect(screen.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    // The trailing block is the one still being written: plain text, or
    // a half-arrived `##` would re-parse on every delta.
    expect(screen.queryByRole('heading', { name: 'Still writing' })).toBeNull();
    expect(screen.getByText('## Still writing')).toBeInTheDocument();

    // The turn ending does not change what either block is.
    act(() => {
      actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    });
    expect(screen.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Still writing' }),
    ).toBeInTheDocument();
  });
});

// The header is the pane's title bar, read top to bottom: whose
// conversation this is, then the policy it runs under and the weight it
// already carries, with whatever the turn is doing pinned right.
describe('ChatView header', () => {
  const session: SessionMeta = {
    id: 's-1',
    title: 'Add the usage hero card',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    turns: 3,
    messages: 6,
    total_tokens: 12000,
  };

  it('names the conversation and reads out the policy it runs under', () => {
    setConversation([]);
    useStore.setState({ sessions: [session] });
    render(<ChatView />);

    expect(screen.getByTestId('chat-title')).toHaveTextContent(session.title);
    expect(screen.getByTestId('chat-mode-mark')).toHaveAttribute(
      'data-mode',
      'workspace',
    );
    expect(screen.getByTestId('chat-mode-label')).toHaveTextContent(
      'Workspace mode',
    );
  });

  it('summarizes the turns and tokens the session already carries', () => {
    setConversation([]);
    useStore.setState({ sessions: [session] });
    render(<ChatView />);

    const meta = screen.getByTestId('chat-meta');
    expect(meta).toHaveTextContent('3 turns');
    expect(meta).toHaveTextContent('12k tokens');
  });

  it('keeps the counts off a session with no archive row yet', () => {
    setConversation([]);
    useStore.setState({ sessions: [] });
    render(<ChatView />);

    const meta = screen.getByTestId('chat-meta');
    expect(meta).toHaveTextContent('Workspace mode');
    expect(meta).not.toHaveTextContent('turns');
  });

  it('tints the policy once it leaves the default', () => {
    setConversation([], [], 'yolo');
    useStore.setState({ sessions: [session] });
    render(<ChatView />);

    const mark = screen.getByTestId('chat-mode-mark');
    expect(mark).toHaveAttribute('data-mode', 'yolo');
    expect(mark.className).toContain('bg-yolo');
    expect(screen.getByTestId('chat-mode-label')).toHaveTextContent('YOLO');
  });

  it('reports a live run and clears it when the turn ends', () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor?.send({ type: 'STREAM', runID: 'r-1', stage: 'tool:exec_command' });
    render(<ChatView />);

    expect(screen.getByTestId('chat-run-state')).toHaveTextContent(
      'Using exec_command…',
    );
    expect(screen.getByTestId('chat-run-sweep')).toBeInTheDocument();

    act(() => {
      actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    });

    expect(screen.queryByTestId('chat-run-state')).not.toBeInTheDocument();
    expect(screen.queryByTestId('chat-run-sweep')).not.toBeInTheDocument();
  });

  it('ticks the elapsed time of the call in flight', () => {
    setConversation([
      {
        id: 'm-1',
        role: 'assistant',
        text: '',
        items: [
          {
            kind: 'tool_call',
            id: 'p-1',
            tool: {
              id: 'call-1',
              name: 'exec_command',
              args: '{"command":"go build ./..."}',
              status: 'running',
              seenAt: Date.now() - 3000,
            },
          },
        ],
        attachments: [],
      },
    ]);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor?.send({ type: 'STREAM', runID: 'r-1', stage: 'tool:exec_command' });
    render(<ChatView />);

    // The same clock the tool group reads, so the bar and the row agree.
    expect(screen.getByTestId('chat-run-elapsed')).toHaveTextContent('3s');
  });

  it('reports a turn that did not finish in the transcript\u2019s own words', () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    act(() => {
      actor?.send({
        type: 'TURN_ENDED',
        runID: 'r-1',
        status: 'failed',
        error: 'inference: request failed: 502 bad gateway',
      });
    });

    const stop = screen.getByTestId('chat-turn-stop');
    expect(stop).toHaveAttribute('data-status', 'failed');
    expect(stop).toHaveTextContent('Last reply failed');
    // The reason itself is a tooltip: the transcript's notice carries it.
    expect(stop).toHaveAttribute(
      'data-tip',
      'inference: request failed: 502 bad gateway',
    );
    expect(screen.queryByTestId('chat-run-sweep')).not.toBeInTheDocument();

    // The next turn takes the line over: a send clears the last one's
    // ending rather than leaving two states stacked in one row.
    act(() => {
      actor?.send({ type: 'SEND_STARTED' });
    });
    expect(screen.queryByTestId('chat-turn-stop')).not.toBeInTheDocument();
    expect(screen.getByTestId('chat-run-state')).toHaveTextContent('Running');
  });

  it('keeps a stop the user asked for quiet', () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    act(() => {
      actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'canceled' });
    });

    const stop = screen.getByTestId('chat-turn-stop');
    expect(stop).toHaveAttribute('data-status', 'canceled');
    expect(stop).toHaveTextContent('Reply cancelled');
    expect(stop.className).toContain('text-dim');
  });

  it('reports a deadline with its own words', () => {
    setConversation([], []);
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    act(() => {
      actor?.send({
        type: 'TURN_ENDED',
        runID: 'r-1',
        status: 'canceled',
        error: 'context deadline exceeded',
        errorKind: 'timeout',
      });
    });

    // The same status as the quiet user stop above, read as a warning:
    // the header mirrors the transcript's notice instead of blaming the
    // user for the run's own deadline.
    const stop = screen.getByTestId('chat-turn-stop');
    expect(stop).toHaveAttribute('data-status', 'canceled');
    expect(stop).toHaveTextContent('Reply timed out');
    expect(stop.className).toContain('text-warn');
    expect(stop).toHaveAttribute('data-tip', 'context deadline exceeded');
  });
});

// The card belongs to the conversation rather than to the work: a turn
// ending does not take it down, and what it does not paint is a card with
// nothing to report. Both halves are the pane's: it mounts the card
// (keyed by the conversation, so switching sessions starts the reader's
// folds over) and the card itself decides whether it has anything to
// show.
describe('ChatView activity card', () => {
  const runningProcess: SandboxProcess = {
    process_id: 'p-1',
    argv: ['npm', 'run', 'dev'],
    workdir: '/tmp/w',
    tty: false,
    pid: 4242,
    started_at: '2026-01-01T00:00:00Z',
    running: true,
    tail: 'VITE ready in 412 ms\n',
    truncated: false,
    seq: 30,
  };

  it('keeps the card up after the turn it reported', () => {
    setConversation(assistantTurn());
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    act(() => {
      useStore.getState().handleEvent({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: {
            type: 'part',
            part: { type: 'reasoning', text: 'Weighing the layout.' },
          },
        },
      });
      useStore.getState().flushStreams();
    });
    expect(screen.getByTestId('activity-card')).toBeInTheDocument();
    expect(screen.getByTestId('think-body')).toHaveTextContent(
      'Weighing the layout.',
    );

    // The turn ends and the card stays: the thought it was reporting is
    // still the newest thing the conversation has to show, so nothing
    // about the card was tied to the turn.
    act(() => {
      actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    });
    expect(screen.getByTestId('activity-card')).toBeInTheDocument();
    expect(screen.getByTestId('think-body')).toHaveTextContent(
      'Weighing the layout.',
    );
    expect(screen.getByText('working on it')).toBeInTheDocument();
  });

  it('mounts the card for a process that runs on after its turn', async () => {
    apiMock.processes.mockResolvedValue([runningProcess]);
    setConversation(assistantTurn());
    render(<ChatView />);

    // No turn is running here: a server an earlier turn started is
    // activity in its own right, so the card reports it and follows it
    // until it stops.
    const card = await screen.findByTestId('activity-card');
    expect(within(card).getByText('npm run dev')).toBeInTheDocument();
    expect(within(card).getByText('running')).toBeInTheDocument();
  });

  it('paints nothing for a conversation whose only process has stopped', async () => {
    apiMock.processes.mockResolvedValue([
      {
        ...runningProcess,
        running: false,
        exit_code: 0,
        exit_reason: 'exited',
      },
    ]);
    setConversation(assistantTurn());
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SEND_STARTED' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    render(<ChatView />);

    // The turn is in flight and the feed reports a process that has
    // already ended: a stopped process is not content, so with no plan
    // and no thought the card paints nothing instead of an empty shell —
    // how the command ended is the transcript's record.
    await act(async () => {});
    expect(apiMock.processes).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('activity-card')).toBeNull();
  });

  it('starts the folds over when the conversation changes', () => {
    setConversation(thoughtTranscript('a', 'First conversation.'));
    render(<ChatView />);
    fireEvent.click(screen.getByTestId('activity-card-header'));
    expect(screen.getByTestId('activity-card')).toHaveAttribute(
      'data-folded',
      'true',
    );

    // Another conversation: the card is the conversation's, and so are
    // its folds — the reader meets the next session with it unfolded
    // rather than with a fold they made somewhere else.
    act(() => {
      stateRoot.sendFocus({ type: 'OPEN_SESSION', id: 's-2' });
    });
    const request = stateRoot.focusSnapshot.context.request;
    act(() => {
      stateRoot.registry
        .ensure('s-2', { workspaceGeneration: stateRoot.generation() })
        ?.send({ type: 'NEW_CHAT_READY' });
      stateRoot.sendFocus({
        type: 'OPEN_SUCCEEDED',
        request,
        sessionID: 's-2',
      });
      useStore.setState({
        conversations: {
          's-2': {
            messages: thoughtTranscript('b', 'Second conversation.'),
            turnArtifacts: [],
            mode: 'workspace',
            think: 'medium',
            model: '',
            pendingInteracts: [],
          },
        },
      });
    });

    expect(screen.getByTestId('activity-card')).toHaveAttribute(
      'data-folded',
      'false',
    );
    expect(screen.getByTestId('think-body')).toHaveTextContent(
      'Second conversation.',
    );
  });
});
