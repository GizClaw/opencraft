import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { SHORTCUTS } from '../../lib/keys';
import { TooltipLayer } from './Tooltip';

// The hint's dismissal contract for scrolls, which is the popover's: a
// pane the hinted control is not inside (the chat transcript following a
// turn, the activity card's tails, a log pane) is none of the hint's
// business, while the control's own container and the page itself take
// the hint down because the control went with them.
function Harness({
  anchorInside = false,
  shortcut,
}: {
  anchorInside?: boolean;
  shortcut?: string;
}) {
  return (
    <div>
      {anchorInside ? (
        <div data-testid="scroller">
          <button
            type="button"
            data-tip="Save the draft"
            data-tip-keys={shortcut}
          >
            Draft
          </button>
        </div>
      ) : (
        <>
          <button
            type="button"
            data-tip="Save the draft"
            data-tip-keys={shortcut}
          >
            Draft
          </button>
          <div data-testid="scroller" />
        </>
      )}
      <TooltipLayer isMac />
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

  it('shows the key of a hinted control, spelled by the table', () => {
    render(<Harness shortcut="turn.stop" />);
    fireEvent.pointerOver(screen.getByRole('button', { name: 'Draft' }));
    act(() => {
      vi.advanceTimersByTime(400);
    });
    // Both combos of the row, formatted for the platform — the hint and
    // the key that fires come from one table (lib/keys.ts).
    expect(screen.getByRole('tooltip')).toHaveTextContent('Esc / ⌘.');
  });

  it('shows no key for a control that names one the table does not have', () => {
    render(<Harness shortcut="nope.gone" />);
    fireEvent.pointerOver(screen.getByRole('button', { name: 'Draft' }));
    act(() => {
      vi.advanceTimersByTime(400);
    });
    const hint = screen.getByRole('tooltip');
    expect(hint).toHaveTextContent('Save the draft');
    expect(hint.querySelector('kbd')).toBeNull();
  });
});

// The hinted keys are ids into the shortcut table, and the attribute is
// spelled out in a dozen components where no type checker sees across the
// DOM boundary — so the table is asked whether every id it is used with
// exists. A typo would otherwise show up as a hint that quietly has no key.
describe('hinted shortcut ids', () => {
  it('all name a row of the table', () => {
    const sources = import.meta.glob('../../**/*.tsx', {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>;
    const used = new Set<string>();
    for (const [path, source] of Object.entries(sources)) {
      if (path.endsWith('.test.tsx')) continue;
      for (const match of source.matchAll(
        /(?:data-tip-keys|shortcut)="([^"]+)"/g,
      )) {
        used.add(match[1]);
      }
    }
    expect(used.size).toBeGreaterThan(0);
    for (const id of used) {
      expect(
        SHORTCUTS.some((spec) => spec.id === id),
        `${id} is not in lib/keys.ts`,
      ).toBe(true);
    }
  });
});
