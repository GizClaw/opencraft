import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { installMemoryLocalStorage } from '../test/storage';
import { SidebarResizeHandle } from './SidebarResizeHandle';

// jsdom has no pointer capture: the browser routes the moves after a
// press back to the pressed element, so the double only has to exist for
// the handlers to call (the real capture is exercised by the visual
// audit, which drags the seam in Chromium).
beforeEach(() => {
  Element.prototype.setPointerCapture = vi.fn();
  Element.prototype.releasePointerCapture = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => true);
});

/** pointer builds the event jsdom has no constructor for. */
function pointer(type: string, clientX: number, pointerId = 1): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.assign(event, { clientX, pointerId, button: 0, buttons: 1 });
  return event;
}

function seam() {
  return screen.getByRole('separator', { name: 'Resize sidebar' });
}

/** press dispatches one leg of a drag and lets React flush what it set. */
function press(type: string, clientX: number) {
  act(() => {
    seam().dispatchEvent(pointer(type, clientX));
  });
}

describe('sidebar resize handle', () => {
  let store: Map<string, string>;

  beforeEach(() => {
    store = installMemoryLocalStorage();
  });

  it('is a focusable vertical separator that reports the column range', () => {
    render(<SidebarResizeHandle value={300} onChange={vi.fn()} />);
    const handle = seam();
    expect(handle).toHaveAttribute('aria-orientation', 'vertical');
    expect(handle).toHaveAttribute('aria-valuenow', '300');
    expect(handle).toHaveAttribute('aria-valuemin', '180');
    expect(handle).toHaveAttribute('aria-valuemax', '480');
    handle.focus();
    expect(handle).toHaveFocus();
  });

  it('reports every move of a drag, clamped to whole pixels', () => {
    const onChange = vi.fn();
    render(<SidebarResizeHandle value={240} onChange={onChange} />);
    press('pointerdown', 240);
    press('pointermove', 300.6);
    press('pointermove', 40);
    expect(onChange.mock.calls).toEqual([[301], [180]]);
  });

  it('writes the stored width once the drag ends, not on every move', () => {
    const setItem = vi.spyOn(window.localStorage, 'setItem');
    render(<SidebarResizeHandle value={300} onChange={vi.fn()} />);
    expect(setItem).toHaveBeenCalledTimes(1); // the value it mounted with
    press('pointerdown', 300);
    press('pointermove', 340);
    press('pointermove', 360);
    expect(setItem).toHaveBeenCalledTimes(1);
    press('pointerup', 360);
    expect(setItem).toHaveBeenCalledTimes(2);
    expect(store.get('oc.sidebarW')).toBe('300');
  });

  it('locks the cursor and the selection for the length of the drag', () => {
    const { unmount } = render(
      <SidebarResizeHandle value={240} onChange={vi.fn()} />,
    );
    expect(seam()).not.toHaveAttribute('data-dragging');
    press('pointerdown', 240);
    expect(document.body).toHaveClass('oc-resizing');
    expect(seam()).toHaveAttribute('data-dragging', 'true');
    press('pointerup', 240);
    expect(document.body).not.toHaveClass('oc-resizing');
    // A tree that goes away mid-drag must not leave the lock behind.
    press('pointerdown', 240);
    act(() => unmount());
    expect(document.body).not.toHaveClass('oc-resizing');
  });

  it('nudges with the arrow keys: 8px a press, 1px with Shift', () => {
    const onChange = vi.fn();
    render(<SidebarResizeHandle value={240} onChange={onChange} />);
    const handle = seam();
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    fireEvent.keyDown(handle, { key: 'ArrowLeft' });
    fireEvent.keyDown(handle, { key: 'ArrowRight', shiftKey: true });
    fireEvent.keyDown(handle, { key: 'ArrowUp' });
    expect(onChange.mock.calls).toEqual([[248], [232], [241]]);
  });

  it('clamps a nudge at the ends of the range', () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <SidebarResizeHandle value={478} onChange={onChange} />,
    );
    fireEvent.keyDown(seam(), { key: 'ArrowRight' });
    expect(onChange).toHaveBeenLastCalledWith(480);
    rerender(<SidebarResizeHandle value={181} onChange={onChange} />);
    fireEvent.keyDown(seam(), { key: 'ArrowLeft' });
    expect(onChange).toHaveBeenLastCalledWith(180);
  });

  it('resets to the default width on a double click', () => {
    const onChange = vi.fn();
    render(<SidebarResizeHandle value={420} onChange={onChange} />);
    fireEvent.doubleClick(seam());
    expect(onChange).toHaveBeenCalledWith(240);
  });
});
