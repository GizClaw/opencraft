import { fireEvent, render, screen } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { describe, expect, it } from 'vitest';
import { Overlay } from './Overlay';

// jsdom answers every layout question with nothing, and the focus trap
// skips elements with no client rects; a real browser gives every visible
// control one.
function withLayout(el: HTMLElement): HTMLElement {
  Object.defineProperty(el, 'getClientRects', {
    configurable: true,
    value: () => ({ length: 1 }),
  });
  return el;
}

// A surface mounted closed and opened by a click, the way the app uses
// every dialog: the palette arrives with ⌘K, a settings dialog with a
// click on its trigger.
function Shell({
  initialFocus,
  children,
}: {
  initialFocus?: string | false;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button type="button" onClick={() => setOpen(true)}>
        Open
      </button>
      <Overlay
        open={open}
        onClose={() => setOpen(false)}
        initialFocus={initialFocus}
      >
        {children}
      </Overlay>
    </div>
  );
}

const open = () =>
  fireEvent.click(screen.getByRole('button', { name: 'Open' }));

describe('Overlay focus', () => {
  it('focuses the initial target when it opens after being mounted closed', () => {
    // The panel used to mount one commit after `open` did, so the focus
    // effect ran against a null ref and never ran again: ⌘K opened the
    // palette onto an unfocused search field.
    render(
      <Shell initialFocus="[data-search]">
        <input data-search aria-label="search" />
        <button type="button">Done</button>
      </Shell>,
    );
    open();
    expect(screen.getByLabelText('search')).toHaveFocus();
  });

  it('prefers the field marked data-autofocus', () => {
    render(
      <Shell>
        <input aria-label="plain" />
        <input data-autofocus aria-label="wanted" />
      </Shell>,
    );
    open();
    expect(screen.getByLabelText('wanted')).toHaveFocus();
  });

  it('focuses the panel itself when nothing inside is marked', () => {
    // Focus left on the body would let Tab escape the trap.
    render(
      <Shell>
        <p>Nothing to type into</p>
        <button type="button">Done</button>
      </Shell>,
    );
    open();
    const panel = screen.getByRole('dialog');
    expect(panel).toHaveFocus();
    expect(panel).toHaveAttribute('tabindex', '-1');
  });

  it('keeps Tab inside the panel', () => {
    render(
      <Shell>
        <input aria-label="first" />
        <button type="button">Last</button>
      </Shell>,
    );
    open();
    const first = withLayout(screen.getByLabelText('first'));
    const last = withLayout(screen.getByRole('button', { name: 'Last' }));

    last.focus();
    fireEvent.keyDown(last, { key: 'Tab' });
    expect(first).toHaveFocus();

    first.focus();
    fireEvent.keyDown(first, { key: 'Tab', shiftKey: true });
    expect(last).toHaveFocus();
  });

  it('gives focus back to the trigger once it closes', () => {
    // The panel stays on screen for its exit animation, so the focus
    // has to leave it while it is still a live element: otherwise the
    // caret dies with the panel and the window is left on the body.
    render(
      <Shell initialFocus="[data-search]">
        <input data-search aria-label="search" />
        <button type="button">Done</button>
      </Shell>,
    );
    const trigger = screen.getByRole('button', { name: 'Open' });
    trigger.focus();
    fireEvent.click(trigger);
    const search = screen.getByLabelText('search');
    expect(search).toHaveFocus();

    fireEvent.keyDown(search, { key: 'Escape' });
    // Still mounted for its exit animation — that is what used to take
    // the focus down with it.
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});
