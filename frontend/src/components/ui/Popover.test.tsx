import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useRef, useState } from 'react';
import { Popover } from './Popover';

// The popover's dismissal contract for scrolls: the panel's own list
// scrolls without closing the menu it is scrolling, a scroller the
// anchor is not inside (the chat transcript, the activity card's tails,
// a log pane) is none of the menu's business, and the page itself moving
// still closes it because the anchor went with it.
function Harness({
  onClose,
  anchorInside,
}: {
  onClose: () => void;
  anchorInside?: boolean;
}) {
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  return (
    <div>
      {anchorInside === true ? (
        <div data-testid="outside-scroller">
          <button ref={triggerRef} type="button" onClick={() => setOpen(true)}>
            Open menu
          </button>
        </div>
      ) : (
        <>
          <button ref={triggerRef} type="button" onClick={() => setOpen(true)}>
            Open menu
          </button>
          <div data-testid="outside-scroller" />
        </>
      )}
      <Popover
        open={open}
        onClose={() => {
          setOpen(false);
          onClose();
        }}
        anchor={triggerRef.current}
        role="menu"
      >
        <button type="button" role="menuitem">
          Option
        </button>
      </Popover>
    </div>
  );
}

function openMenu() {
  const onClose = vi.fn();
  render(<Harness onClose={onClose} />);
  fireEvent.click(screen.getByRole('button', { name: 'Open menu' }));
  expect(screen.getByRole('menuitem')).toBeInTheDocument();
  return onClose;
}

describe('Popover scroll dismissal', () => {
  it('ignores a scroll from a container the anchor is not in', () => {
    const onClose = openMenu();
    fireEvent.scroll(screen.getByTestId('outside-scroller'));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('menuitem')).toBeInTheDocument();
  });

  it('closes when the anchor scrolls with its own container', () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} anchorInside />);
    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }));
    fireEvent.scroll(screen.getByTestId('outside-scroller'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes when the page itself scrolls', () => {
    const onClose = openMenu();
    fireEvent.scroll(document);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps its own list scrolling', () => {
    const onClose = openMenu();
    fireEvent.scroll(screen.getByRole('menuitem'));
    expect(onClose).not.toHaveBeenCalled();
  });
});
