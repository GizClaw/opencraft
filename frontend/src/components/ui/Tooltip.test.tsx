import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { TooltipLayer } from './Tooltip';

// The hint's dismissal contract for scrolls, which is the popover's: a
// pane the hinted control is not inside (the chat transcript following a
// turn, the activity card's tails, a log pane) is none of the hint's
// business, while the control's own container and the page itself take
// the hint down because the control went with them.
function Harness({ anchorInside = false }: { anchorInside?: boolean }) {
  return (
    <div>
      {anchorInside ? (
        <div data-testid="scroller">
          <button type="button" data-tip="Save the draft">
            Draft
          </button>
        </div>
      ) : (
        <>
          <button type="button" data-tip="Save the draft">
            Draft
          </button>
          <div data-testid="scroller" />
        </>
      )}
      <TooltipLayer />
    </div>
  );
}

// hintOn shows a hint the way the layer does it: a pointer arriving on a
// hinted control, then the hover delay elapsing.
function hintOn(anchorInside = false) {
  render(<Harness anchorInside={anchorInside} />);
  fireEvent.pointerOver(screen.getByRole('button', { name: 'Draft' }));
  act(() => {
    vi.advanceTimersByTime(400);
  });
  expect(screen.getByRole('tooltip')).toBeInTheDocument();
}

describe('Tooltip scroll dismissal', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('ignores a scroll from a container the control is not in', () => {
    hintOn();
    fireEvent.scroll(screen.getByTestId('scroller'));
    expect(screen.getByRole('tooltip')).toBeInTheDocument();
  });

  it('hides when the control scrolls with its own container', () => {
    hintOn(true);
    fireEvent.scroll(screen.getByTestId('scroller'));
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('hides when the page itself scrolls', () => {
    hintOn();
    fireEvent.scroll(document);
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  // The hint is scheduled, not shown, for the length of the hover delay:
  // a scroll that moved the control in that window still has to drop it.
  it('drops a hint whose control scrolls away while it waits', () => {
    render(<Harness anchorInside />);
    fireEvent.pointerOver(screen.getByRole('button', { name: 'Draft' }));
    fireEvent.scroll(screen.getByTestId('scroller'));
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('still hides when the pointer leaves the control', () => {
    hintOn();
    fireEvent.pointerOut(screen.getByRole('button', { name: 'Draft' }), {
      relatedTarget: document.body,
    });
    expect(screen.queryByRole('tooltip')).toBeNull();
  });
});
