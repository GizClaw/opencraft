import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { stateRoot } from '../state/app';
import { MessagePeek, type MessagePeekItem } from './MessagePeek';

const apiMock = vi.hoisted(() => ({
  resolveTarget: vi.fn(),
  openExternal: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const turns: MessagePeekItem[] = [{ index: 0 }, { index: 1 }];
const previews = [
  { user: 'Add search', answer: 'Search **added**.', running: false },
  { user: 'Add sorting', answer: '', running: true },
];

const previewFor = (index: number) => previews[index];

describe('MessagePeek', () => {
  beforeEach(() => {
    apiMock.resolveTarget.mockReset();
    apiMock.openExternal.mockReset();
    // The preview's links open into the active conversation's file
    // panel, so the store needs one focus snapshot to resolve against.
    stateRoot.resetWorkspace();
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    stateRoot.registry.ensure('s-1', {
      workspaceGeneration: stateRoot.generation(),
      readyEmpty: true,
      workspace: '/tmp/w',
    });
    useStore.setState({ viewers: {} });
  });

  it('renders one tick per turn and highlights the current turn', () => {
    render(
      <MessagePeek
        items={turns}
        activeRange={{ start: 0, end: 0 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    expect(screen.getByTestId('message-peek')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Jump to turn 2' }),
    ).toBeInTheDocument();
  });

  it('highlights every turn visible in the viewport', () => {
    const manyTurns = [{ index: 0 }, { index: 1 }, { index: 2 }];
    render(
      <MessagePeek
        items={manyTurns}
        activeRange={{ start: 0, end: 2 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    for (const label of [
      'Jump to turn 1',
      'Jump to turn 2',
      'Jump to turn 3',
    ]) {
      const button = screen.getByRole('button', { name: label });
      expect(button.querySelector('span')?.className).toContain('bg-accent');
    }
  });

  it('loads previews lazily on hover and renders markdown', async () => {
    vi.useFakeTimers();
    const getPreview = vi.fn(previewFor);
    render(
      <MessagePeek
        items={turns}
        activeRange={{ start: 0, end: 0 }}
        onJump={vi.fn()}
        getPreview={getPreview}
      />,
    );
    expect(getPreview).not.toHaveBeenCalled();

    fireEvent.mouseEnter(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(getPreview).toHaveBeenCalledWith(0);
    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('Add search');
    expect(tooltip).toHaveTextContent('Search added.');
    expect(tooltip).not.toHaveTextContent('Turn 1');
    expect(tooltip).toHaveTextContent('Assistant');
    expect(tooltip.querySelector('strong')).toHaveTextContent('added');

    fireEvent.mouseLeave(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    );
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    vi.useRealTimers();
  });

  it('calls onJump when a tick is clicked', () => {
    const onJump = vi.fn();
    render(
      <MessagePeek
        items={turns}
        activeRange={{ start: 0, end: 0 }}
        onJump={onJump}
        getPreview={previewFor}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Jump to turn 2' }));

    expect(onJump).toHaveBeenCalledWith(1);
  });

  it('opens a link from the hover preview in the chat file panel', async () => {
    apiMock.resolveTarget.mockResolvedValue({
      path: '/tmp/w/docs/guide.md',
      rel: 'docs/guide.md',
      root: 'workspace',
      name: 'guide.md',
      is_dir: false,
      size: 12,
      media_type: 'text/markdown',
    });
    render(
      <MessagePeek
        items={[{ index: 0 }]}
        activeRange={{ start: 0, end: 0 }}
        onJump={vi.fn()}
        getPreview={() => ({
          user: 'Add search',
          answer: 'See [guide](docs/guide.md) for the search API.',
          running: false,
        })}
      />,
    );

    fireEvent.mouseEnter(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    );
    fireEvent.click(await screen.findByRole('link', { name: 'guide' }));

    // Chat surfaces share one rule: the file opens as a viewer tab of
    // the session, never as a dialog of its own.
    await waitFor(() =>
      expect(useStore.getState().viewers['s-1']?.fileActive).toBe(
        '/tmp/w/docs/guide.md',
      ),
    );
    expect(apiMock.resolveTarget).toHaveBeenCalledWith('docs/guide.md', '');
    expect(apiMock.openExternal).not.toHaveBeenCalled();
  });

  it('renders nothing without turns', () => {
    render(
      <MessagePeek
        items={[]}
        activeRange={null}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    expect(screen.queryByTestId('message-peek')).not.toBeInTheDocument();
  });

  it('switches to a keyboard-reachable scrubber for dense sessions', async () => {
    vi.useFakeTimers();
    const denseTurns = Array.from({ length: 70 }, (_, i) => ({ index: i }));
    const getPreview = vi.fn((index: number) => ({
      user: `user-${index}`,
      answer: `answer-${index}`,
      running: false,
    }));
    const onJump = vi.fn();
    render(
      <MessagePeek
        items={denseTurns}
        activeRange={{ start: 0, end: 0 }}
        onJump={onJump}
        getPreview={getPreview}
      />,
    );

    const scrubber = screen.getByRole('slider', {
      name: 'Message peek: turn scrubber',
    });
    fireEvent.keyDown(scrubber, { key: 'End' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('user-69');
    expect(tooltip).toHaveTextContent('answer-69');
    expect(tooltip).not.toHaveTextContent('Turn 70');

    fireEvent.keyDown(scrubber, { key: 'Enter' });
    expect(onJump).toHaveBeenCalledWith(69);
    vi.useRealTimers();
  });

  it('draws one dash per turn on a dense ruler that still has room', () => {
    const turns42 = Array.from({ length: 42 }, (_, i) => ({ index: i }));
    const { container } = render(
      <MessagePeek
        items={turns42}
        activeRange={{ start: 41, end: 41 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    const dashes = container.querySelectorAll<HTMLElement>('[data-peek-tick]');
    expect(dashes).toHaveLength(42);
    expect(dashes[41].className).toContain('bg-accent');
    expect(dashes[40].className).not.toContain('bg-accent');
  });

  it('buckets turns into even stops once the dense ruler is at its cap', () => {
    const manyTurns = Array.from({ length: 140 }, (_, i) => ({ index: i }));
    const { container } = render(
      <MessagePeek
        items={manyTurns}
        activeRange={{ start: 139, end: 139 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    const dashes = container.querySelectorAll<HTMLElement>('[data-peek-tick]');
    expect(dashes).toHaveLength(48);
    // The last stop lands on the newest turn instead of a fixed stride
    // running past it, so the accent tracks the bottom of the ruler.
    expect(dashes[47].dataset.peekTick).toBe('137');
    expect(dashes[47].className).toContain('bg-accent');
    expect(dashes[46].className).not.toContain('bg-accent');
  });
});
