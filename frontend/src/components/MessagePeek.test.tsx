import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MessagePeek, type MessagePeekItem } from './MessagePeek';

const turns: MessagePeekItem[] = [
  {
    index: 0,
    user: 'Add search',
    answer: 'Search **added**.',
    running: false,
  },
  { index: 1, user: 'Add sorting', answer: '', running: true },
];

describe('MessagePeek', () => {
  it('renders one tick per turn and highlights the current turn', () => {
    render(<MessagePeek items={turns} currentIndex={0} onJump={vi.fn()} />);

    expect(screen.getByTestId('message-peek')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Jump to turn 2' }),
    ).toBeInTheDocument();
  });

  it('shows the user request and final assistant answer on hover', () => {
    render(<MessagePeek items={turns} currentIndex={0} onJump={vi.fn()} />);

    fireEvent.mouseEnter(
      screen.getByRole('button', { name: 'Jump to turn 1' }),
    );

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
  });

  it('calls onJump when a tick is clicked', () => {
    const onJump = vi.fn();
    render(<MessagePeek items={turns} currentIndex={0} onJump={onJump} />);

    fireEvent.click(screen.getByRole('button', { name: 'Jump to turn 2' }));

    expect(onJump).toHaveBeenCalledWith(1);
  });

  it('renders nothing without turns', () => {
    render(<MessagePeek items={[]} currentIndex={-1} onJump={vi.fn()} />);

    expect(screen.queryByTestId('message-peek')).not.toBeInTheDocument();
  });

  it('switches to a keyboard-reachable scrubber for dense sessions', () => {
    const denseTurns = Array.from({ length: 70 }, (_, i) => ({
      index: i,
      user: `user-${i}`,
      answer: `answer-${i}`,
      running: false,
    }));
    const onJump = vi.fn();
    render(<MessagePeek items={denseTurns} currentIndex={0} onJump={onJump} />);

    const scrubber = screen.getByRole('slider', {
      name: 'Message peek: turn scrubber',
    });
    fireEvent.keyDown(scrubber, { key: 'End' });
    const tooltip = screen.getByRole('tooltip');
    expect(tooltip).toHaveTextContent('user-69');
    expect(tooltip).toHaveTextContent('answer-69');
    expect(tooltip).not.toHaveTextContent('Turn 70');

    fireEvent.keyDown(scrubber, { key: 'Enter' });
    expect(onJump).toHaveBeenCalledWith(69);
  });
});
