import { fireEvent, render, screen } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
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

  it('stays open when the Escape was the IME dropping a candidate', () => {
    // Cancelling a composition ends it with Escape on both sides of the
    // IME: the keydown while composing, and — on Chromium/WebView2 — a
    // stray one after compositionend (lib/ime.ts). Neither is a request
    // to close the dialog the user is typing into.
    render(
      <Shell initialFocus="[data-search]">
        <input data-search aria-label="search" />
      </Shell>,
    );
    open();
    const search = screen.getByLabelText('search');

    fireEvent.keyDown(search, { key: 'Escape', isComposing: true });
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    fireEvent.compositionEnd(search);
    fireEvent.keyDown(search, { key: 'Escape' });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});

describe('Overlay dismissal', () => {
  // The scrim is the surface's own element; a click that lands on it is
  // the one the browser reports for "pressed inside, released outside".
  const scrimOf = (panel: HTMLElement) => panel.parentElement as HTMLElement;

  /** renderOpen mounts one open dialog and returns its scrim and close spy. */
  function renderOpen() {
    const onClose = vi.fn();
    render(
      <Overlay open onClose={onClose}>
        <p>Selectable text</p>
      </Overlay>,
    );
    return { onClose, scrim: scrimOf(screen.getByRole('dialog')) };
  }

  it('stays open when a press inside the panel ends on the scrim', () => {
    // Selecting text inside a dialog and releasing past its edge is not a
    // request to close it (and to throw away whatever was typed into it).
    // The guard used to be a render-time copy of the press flag, which
    // never changed without a re-render, so the dialog closed on every
    // drag out of a selection.
    const { onClose, scrim } = renderOpen();

    fireEvent.mouseDown(screen.getByText('Selectable text'));
    fireEvent.click(scrim);
    expect(onClose).not.toHaveBeenCalled();

    // The press is consumed, not sticky: the next click on the scrim —
    // this time with no press inside the panel — closes the dialog.
    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on a click that starts and ends on the scrim', () => {
    // The counterpart of the guard above: it must not turn into "clicks
    // outside never close anything".
    const { onClose, scrim } = renderOpen();

    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
