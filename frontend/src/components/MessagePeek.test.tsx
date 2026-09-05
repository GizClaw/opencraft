import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MessagePeek, type MessagePeekItem } from './MessagePeek';

const turns: MessagePeekItem[] = [{ index: 0 }, { index: 1 }];
const previews = [
  { user: 'Add search', answer: 'Search **added**.', running: false },
  { user: 'Add sorting', answer: '', running: true },
];

const previewFor = (index: number) => previews[index];

describe('MessagePeek', () => {
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
});
