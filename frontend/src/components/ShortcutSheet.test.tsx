import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { useStore } from '../lib/store';
import { KEY_REFERENCES, SHORTCUTS } from '../lib/keys';
import { ShortcutSheet } from './ShortcutSheet';

// The sheet is a rendering of the keyboard table, so its test is mostly a
// question of whether the table made it onto the screen — the reason the
// sheet exists is that a fixed key map with no reference is a set of keys
// nobody can find.
beforeEach(() => {
  useStore.setState({ shortcutsOpen: true });
});

function renderSheet(isMac = true) {
  return render(<ShortcutSheet isMac={isMac} />);
}

describe('ShortcutSheet', () => {
  it('lists every command the shell dispatches', () => {
    renderSheet();
    const sheet = screen.getByTestId('shortcut-sheet');
    // Spot-check one row per group rather than restating the table: the
    // point is that each group is drawn, not that the copy is.
    expect(sheet).toHaveTextContent('Command palette');
    expect(sheet).toHaveTextContent('Stop the running reply');
    expect(sheet).toHaveTextContent('Focus the composer');
    expect(sheet).toHaveTextContent('Previous session');
    expect(sheet).toHaveTextContent('Keyboard shortcuts');
  });

  it('documents the keys a surface owns as well', () => {
    renderSheet();
    const sheet = screen.getByTestId('shortcut-sheet');
    // Enter in the composer, Space in a prompt, ↑↓ in the ruler: none of
    // them are the shell's to dispatch, and all of them are things a user
    // wants to look up.
    expect(sheet).toHaveTextContent('Send · steers the reply that is running');
    expect(sheet).toHaveTextContent('Pick the option under the cursor');
    expect(sheet).toHaveTextContent('Walk the turns in the ruler');
  });

  it('renders every row of the table', () => {
    renderSheet();
    const rows = screen
      .getByTestId('shortcut-sheet')
      .querySelectorAll('section > div');
    expect(rows.length).toBe(SHORTCUTS.length + KEY_REFERENCES.length);
  });

  it('spells the keys for the platform it is on', () => {
    const { unmount } = renderSheet(true);
    expect(screen.getByTestId('shortcut-sheet')).toHaveTextContent('⌘K');
    expect(screen.getByTestId('shortcut-sheet')).toHaveTextContent('⇧⌘T');
    unmount();

    // The same table on Windows and Linux reads as words.
    renderSheet(false);
    expect(screen.getByTestId('shortcut-sheet')).toHaveTextContent('Ctrl+K');
    expect(screen.getByTestId('shortcut-sheet')).toHaveTextContent(
      'Ctrl+Shift+T',
    );
    expect(screen.getByTestId('shortcut-sheet')).not.toHaveTextContent('⌘');
  });

  it('closes on Escape', async () => {
    renderSheet();
    expect(useStore.getState().shortcutsOpen).toBe(true);
    // Escape belongs to the overlay stack, which the sheet registers as a
    // layer; the shared listener is a capture-phase window listener.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await waitFor(() => expect(useStore.getState().shortcutsOpen).toBe(false));
  });
});
