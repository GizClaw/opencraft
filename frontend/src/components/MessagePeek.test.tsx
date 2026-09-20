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
    // Arrows walk the block one turn at a time, and the preview follows
    // the cursor.
    fireEvent.keyDown(scrubber, { key: 'ArrowDown' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('user-1');
    expect(tooltip).toHaveTextContent('answer-1');
    expect(tooltip).not.toHaveTextContent('Turn 2');

    fireEvent.keyDown(scrubber, { key: 'Enter' });
    expect(onJump).toHaveBeenCalledWith(1);

    // Home/End leave the window: they scroll the transcript to the ends
    // of the conversation instead of moving a cursor the block cannot
    // draw.
    fireEvent.keyDown(scrubber, { key: 'End' });
    expect(onJump).toHaveBeenCalledWith(69);
    vi.useRealTimers();
  });

  it('draws the slice around the viewport, one dash per turn', () => {
    const manyTurns = Array.from({ length: 140 }, (_, i) => ({ index: i }));
    const { container } = render(
      <MessagePeek
        items={manyTurns}
        activeRange={{ start: 69, end: 71 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    const dashes = container.querySelectorAll<HTMLElement>('[data-peek-tick]');
    // 41 turns around the anchor rather than all 140: the block stays
    // the compact ruler instead of a hatch the column has to clip.
    expect(dashes).toHaveLength(41);
    expect(dashes[0].dataset.peekTick).toBe('50');
    expect(dashes[40].dataset.peekTick).toBe('90');
    // The turns on screen are the accent dashes, and they sit in the
    // middle of the block.
    const accent = Array.from(dashes)
      .filter((dash) => dash.className.includes('bg-accent'))
      .map((dash) => dash.dataset.peekTick);
    expect(accent).toEqual(['69', '70', '71']);
    // Cut at both ends: the transcript keeps going in either direction.
    expect(
      container.querySelector('[data-peek-cut]')?.getAttribute('data-peek-cut'),
    ).toBe('both');
  });

  it('clamps the slice at the newest turn and fades only the cut end', () => {
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
    // The window stops at the last turn instead of running past it, so
    // the block is short, the newest turn is its bottom dash, and only
    // the top was cut.
    expect(dashes).toHaveLength(21);
    expect(dashes[0].dataset.peekTick).toBe('119');
    expect(dashes[20].dataset.peekTick).toBe('139');
    expect(dashes[20].className).toContain('bg-accent');
    expect(dashes[19].className).not.toContain('bg-accent');
    expect(
      container.querySelector('[data-peek-cut]')?.getAttribute('data-peek-cut'),
    ).toBe('top');
  });

  it('draws every turn when the whole conversation fits the window', () => {
    const turns41 = Array.from({ length: 41 }, (_, i) => ({ index: i }));
    const { container } = render(
      <MessagePeek
        items={turns41}
        activeRange={{ start: 20, end: 20 }}
        onJump={vi.fn()}
        getPreview={previewFor}
      />,
    );

    const dashes = container.querySelectorAll<HTMLElement>('[data-peek-tick]');
    expect(dashes).toHaveLength(41);
    expect(dashes[0].dataset.peekTick).toBe('0');
    expect(dashes[40].dataset.peekTick).toBe('40');
    // Nothing was cut, so the block keeps hard edges all around.
    expect(
      container.querySelector('[data-peek-cut]')?.getAttribute('data-peek-cut'),
    ).toBe('none');
  });

  it('maps the pointer onto the turns the slice draws', () => {
    const manyTurns = Array.from({ length: 140 }, (_, i) => ({ index: i }));
    const rect = {
      top: 200,
      height: 205,
      bottom: 405,
      left: 0,
      right: 0,
      width: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect;
    const spy = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue(rect);
    const onJump = vi.fn();
    render(
      <MessagePeek
        items={manyTurns}
        activeRange={{ start: 69, end: 71 }}
        onJump={onJump}
        getPreview={previewFor}
      />,
    );

    const scrubber = screen.getByRole('slider', {
      name: 'Message peek: turn scrubber',
    });
    // A quarter of the way down the block is a quarter of the way
    // through its 41 turns: 50 + floor(41 / 4) = 60. The pointer used to
    // be mapped across the whole conversation instead.
    fireEvent.click(scrubber, { clientY: 200 + 205 / 4 });

    expect(onJump).toHaveBeenCalledWith(60);
    spy.mockRestore();
  });
});
