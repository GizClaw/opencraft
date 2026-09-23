// The shell's keyboard listener is the one place a keystroke becomes a
// command, so what it does with the *event* matters as much as which command
// it picks: it sees the key before the focused surface does (capture phase)
// and has to keep it there, or StarterKit's link mark and the scroll
// containers act on the same keystroke.
import { fireEvent, render, renderHook, screen } from '@testing-library/react';
import { useRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { useShortcuts } from './useShortcuts';
import { overlayLayerOpen, useOverlayLayer } from './overlay';

/** press dispatches one keydown from `target` (defaults to the window). */
function press(
  init: KeyboardEventInit,
  target: EventTarget = window,
): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    bubbles: true,
    cancelable: true,
    ...init,
  });
  target.dispatchEvent(event);
  return event;
}

describe('useShortcuts', () => {
  it('runs the command a combo belongs to', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    press({ key: 'k', code: 'KeyK', metaKey: true });
    expect(run).toHaveBeenCalledWith('palette.open');
  });

  it('keeps the key out of the surface underneath', () => {
    // ⌘K toggles a link mark in the editor and ⌘↑ scrolls a container, so a
    // command the shell takes has to consume the event on its way past.
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    const target = document.createElement('div');
    document.body.append(target);
    const seen = vi.fn();
    target.addEventListener('keydown', seen);

    const event = press({ key: 'k', code: 'KeyK', metaKey: true }, target);
    expect(event.defaultPrevented).toBe(true);
    expect(seen).not.toHaveBeenCalled();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('leaves keys it does not own alone', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    const event = press({ key: 'k', code: 'KeyK', ctrlKey: true });
    // ⌃K is a caret motion on macOS, not a command.
    expect(event.defaultPrevented).toBe(false);
    expect(run).not.toHaveBeenCalled();
  });

  it('reaches the shell from inside a text field, and stands down where the field owns the key', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    render(<textarea aria-label="field" />);
    const field = screen.getByLabelText('field');
    field.focus();

    fireEvent.keyDown(field, { key: 'k', code: 'KeyK', metaKey: true });
    expect(run).toHaveBeenCalledWith('palette.open');

    run.mockClear();
    // ⌘↑ walks a textarea to its start on macOS, so the transcript walk
    // stays out of fields (see lib/keys.ts).
    fireEvent.keyDown(field, {
      key: 'ArrowUp',
      code: 'ArrowUp',
      metaKey: true,
    });
    expect(run).not.toHaveBeenCalled();
  });

  it('ignores a key that belongs to an open overlay unless the command runs there', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    const { rerender } = render(<Layer open />);
    expect(overlayLayerOpen()).toBe(true);

    press({ key: 'o', code: 'KeyO', metaKey: true });
    expect(run).not.toHaveBeenCalled();

    // Opening another surface runs (it closes the overlay on its way).
    press({ key: 'n', code: 'KeyN', metaKey: true });
    expect(run).toHaveBeenCalledWith('chat.new');

    rerender(<Layer open={false} />);
    expect(overlayLayerOpen()).toBe(false);
  });

  it('ignores auto-repeat except where a row asks for it', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    press({ key: 'n', code: 'KeyN', metaKey: true, repeat: true });
    expect(run).not.toHaveBeenCalled();
    press({ key: 'ArrowDown', code: 'ArrowDown', metaKey: true, repeat: true });
    expect(run).toHaveBeenCalledWith('chat.nextUser');
  });

  it('never fires mid-composition', () => {
    const run = vi.fn();
    renderHook(() => useShortcuts(run, true));
    press({
      key: 'k',
      code: 'KeyK',
      metaKey: true,
      isComposing: true,
    } as never);
    press({ key: 'k', code: 'KeyK', metaKey: true });
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('runs the newest handler without re-registering the listener', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(
      ({ run }: { run: (id: string) => void }) => useShortcuts(run, true),
      { initialProps: { run: first } },
    );
    press({ key: 'k', code: 'KeyK', metaKey: true });
    expect(first).toHaveBeenCalledWith('palette.open');

    rerender({ run: second });
    press({ key: 'k', code: 'KeyK', metaKey: true });
    expect(second).toHaveBeenCalledWith('palette.open');
  });
});

// Layer registers an overlay the way a surface does: the registry has no
// public open/close, only the hook a surface calls while it is up.
function Layer({ open }: { open: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  useOverlayLayer({
    active: open,
    containerRef: ref,
    onDismiss: () => {},
    lock: false,
    trap: false,
  });
  return <div ref={ref} />;
}
