// The reveal state behind the session-slot numbers (lib/modifierHeld.ts):
// the modifier each platform binds, the pressed state surviving the combo's
// other key, and the two ways a held key must not survive — a keyup that
// never arrives and the keyboard leaving the window.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useModifierHeld } from './modifierHeld';

function Probe({ isMac }: { isMac: boolean }) {
  const held = useModifierHeld(isMac);
  return <span data-testid="modifier">{held ? 'held' : 'up'}</span>;
}

const shown = () => screen.getByTestId('modifier').textContent;

describe('useModifierHeld', () => {
  it('follows the modifier its platform binds and ignores the other one', () => {
    const { unmount } = render(<Probe isMac={false} />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(shown()).toBe('up');
    fireEvent.keyDown(window, { key: 'Control', ctrlKey: true });
    expect(shown()).toBe('held');
    fireEvent.keyUp(window, { key: 'Control' });
    expect(shown()).toBe('up');
    unmount();

    // On macOS ⌃ belongs to the text fields' Emacs-style caret motions, so
    // it is not the reveal key there either.
    render(<Probe isMac />);
    fireEvent.keyDown(window, { key: 'Control', ctrlKey: true });
    expect(shown()).toBe('up');
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(shown()).toBe('held');
  });

  it('stays down while the combo other key is pressed and released', () => {
    render(<Probe isMac />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    fireEvent.keyDown(window, { key: '1', code: 'Digit1', metaKey: true });
    fireEvent.keyUp(window, { key: '1', code: 'Digit1', metaKey: true });
    expect(shown()).toBe('held');
    fireEvent.keyUp(window, { key: 'Meta' });
    expect(shown()).toBe('up');
  });

  it('takes the next event as the truth when the keyup never arrives', () => {
    // ⌘-Tab, Spotlight, Mission Control: the release goes to whatever took
    // the keyboard, so the hints would stay on for good if the pressed state
    // depended on ever seeing the up.
    render(<Probe isMac />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(shown()).toBe('held');
    fireEvent.keyDown(window, { key: 'a', code: 'KeyA' });
    expect(shown()).toBe('up');
  });

  it('clears when the window loses focus or the page is hidden', () => {
    render(<Probe isMac />);
    for (const away of ['blur', 'visibilitychange']) {
      fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
      act(() => {
        if (away === 'blur') window.dispatchEvent(new Event('blur'));
        else document.dispatchEvent(new Event('visibilitychange'));
      });
      expect(shown()).toBe('up');
    }
  });

  it('does not wake its readers for keys that leave the modifier alone', () => {
    let renders = 0;
    function Counting() {
      renders += 1;
      useModifierHeld(true);
      return null;
    }
    render(<Counting />);
    expect(renders).toBe(1);
    for (const key of ['a', 'Enter', 'ArrowDown']) {
      fireEvent.keyDown(window, { key, code: `Key${key[0]}` });
    }
    fireEvent.keyUp(window, { key: 'a', code: 'KeyA' });
    expect(renders).toBe(1);
  });

  it('starts a new reader from "not held"', () => {
    // Whatever was true when the last reader left may have ended with nobody
    // listening; a fresh surface must not open with the hints already on.
    const first = render(<Probe isMac />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(shown()).toBe('held');
    first.unmount();
    render(<Probe isMac />);
    expect(shown()).toBe('up');
  });
});
