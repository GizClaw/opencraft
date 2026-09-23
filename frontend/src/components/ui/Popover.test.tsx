import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  it('gives focus back to its trigger when it closes', async () => {
    // A keyboard user who opens a menu must not lose their place when
    // it closes (this is the contract useOverlayLayer documents).
    const user = userEvent.setup();
    render(<Harness onClose={() => {}} />);
    const trigger = screen.getByRole('button', { name: 'Open menu' });
    await user.click(trigger);
    expect(screen.getByRole('menuitem')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('does not pull focus back when the close came from another control', async () => {
    // Clicking a control outside the menu closes it; focus belongs to
    // whatever the user just clicked, not to the trigger.
    function HarnessWithOther() {
      const triggerRef = useRef<HTMLButtonElement | null>(null);
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button ref={triggerRef} type="button" onClick={() => setOpen(true)}>
            Open menu
          </button>
          <button type="button" data-testid="other-control">
            Other control
          </button>
          <Popover
            open={open}
            onClose={() => setOpen(false)}
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
    const user = userEvent.setup();
    render(<HarnessWithOther />);
    await user.click(screen.getByRole('button', { name: 'Open menu' }));
    expect(screen.getByRole('menuitem')).toBeInTheDocument();
    const other = screen.getByTestId('other-control');
    await user.click(other);
    await waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull());
    expect(other).toHaveFocus();
  });
});
