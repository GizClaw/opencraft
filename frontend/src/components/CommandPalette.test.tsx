import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { CommandPalette } from './CommandPalette';

// The palette reads the store directly, so the test drives it through the
// same surface the app does: open it, type, move, run.
beforeEach(() => {
  useStore.setState({
    paletteOpen: true,
    theme: 'dark',
    workspace: '/tmp/a',
    workspaces: [
      { id: 'w-a', path: '/tmp/a', title: 'Alpha', last_opened: '' },
      { id: 'w-b', path: '/tmp/b', title: 'Beta', last_opened: '' },
    ],
    sessions: [],
  });
});

// The palette takes the platform and the command runner from the shell
// (the badge text and the actions that belong to the shortcut table), so
// the test hands it the same two things the app does.
const runShortcut = vi.fn();
function renderPalette() {
  return render(<CommandPalette isMac runShortcut={runShortcut} />);
}

describe('CommandPalette', () => {
  it('moves focus into the search field when the shortcut opens it', () => {
    // The app mounts the palette closed and opens it with ⌘K, so the
    // caret has to land in the commit that brings the panel up.
    useStore.setState({ paletteOpen: false });
    renderPalette();
    expect(screen.queryByRole('combobox')).toBeNull();

    act(() => useStore.setState({ paletteOpen: true }));
    expect(screen.getByRole('combobox')).toHaveFocus();
  });

  it('lists commands and filters them as the query changes', async () => {
    renderPalette();
    const input = screen.getByRole('combobox');

    expect(screen.getByText('Usage')).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'beta' } });
    await waitFor(() => expect(screen.queryByText('Usage')).toBeNull());
    expect(screen.getByText('Beta')).toBeInTheDocument();
  });

  it('runs the highlighted command on Enter', () => {
    const openConfig = vi.fn();
    useStore.setState({ openConfig });
    renderPalette();

    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'usage' },
    });
    const option = screen.getByRole('option', { name: /Usage/ });
    expect(option).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(openConfig).toHaveBeenCalledWith('usage');
    expect(useStore.getState().paletteOpen).toBe(false);
  });

  it('moves the selection with the arrow keys before running', () => {
    const setTheme = vi.fn();
    useStore.setState({ setTheme });
    renderPalette();

    // Two theme commands exist (light, auto) in this state; the arrow keys
    // walk the list instead of the pointer.
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'theme' },
    });
    const options = screen.getAllByRole('option');
    expect(options[0]).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'ArrowDown' });
    const after = screen.getAllByRole('option');
    expect(after[0]).toHaveAttribute('aria-selected', 'false');
    expect(after[1]).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(setTheme).toHaveBeenCalledWith('auto');
  });

  it('says so when nothing matches instead of showing an empty list', async () => {
    renderPalette();
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'zzzz' },
    });
    await waitFor(() =>
      expect(screen.getByText('Nothing matches')).toBeInTheDocument(),
    );
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('closes when the overlay dismisses, and renders nothing while closed', async () => {
    const { container } = renderPalette();
    expect(screen.getByRole('combobox')).toBeInTheDocument();

    useStore.setState({ paletteOpen: false });
    await waitFor(() =>
      expect(container.querySelector('[role="dialog"]')).toBeNull(),
    );
  });

  it('shows the keys of the commands that have them', () => {
    renderPalette();
    // The badge is rendered from the shortcut table, so the palette and
    // the dispatcher cannot disagree about which key runs a command.
    expect(screen.getByRole('option', { name: /New chat/ })).toHaveTextContent(
      '⌘N',
    );
    expect(
      screen.getByRole('option', { name: /Browse files/ }),
    ).toHaveTextContent('⌘O');
    expect(
      screen.getByRole('option', { name: /Keyboard shortcuts/ }),
    ).toHaveTextContent('⌘/');
    expect(
      screen.getByRole('option', { name: /Copy the last reply/ }),
    ).toHaveTextContent('⇧⌘C');
  });

  it('runs the shortcut-backed commands through the shell runner', () => {
    renderPalette();
    runShortcut.mockClear();
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'keyboard shortcuts' },
    });
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(runShortcut).toHaveBeenCalledWith('shortcuts.open');
  });

  it('holds the results still while a composition is in flight', () => {
    renderPalette();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'usage' } });
    expect(screen.getByText('Usage')).toBeInTheDocument();

    // The letters of a composition are not a search term yet: filtering
    // them would empty the list under the candidate window and re-fill it
    // when the characters land.
    fireEvent.compositionStart(input);
    fireEvent.change(input, { target: { value: 'beta' } });
    expect(input).toHaveValue('beta');
    expect(screen.getByText('Usage')).toBeInTheDocument();
    expect(screen.queryByText('Beta')).toBeNull();

    // Committing filters by what the composition landed on.
    fireEvent.compositionEnd(input);
    expect(screen.getByText('Beta')).toBeInTheDocument();
    expect(screen.queryByText('Usage')).toBeNull();
  });

  it('does not run a command on the Enter that ends a composition', () => {
    const openConfig = vi.fn();
    useStore.setState({ openConfig });
    renderPalette();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'usage' } });
    expect(screen.getByRole('option', { name: /Usage/ })).toHaveAttribute(
      'aria-selected',
      'true',
    );

    // Confirming a candidate: compositionend first, the keydown second
    // (Chromium, and therefore WebView2 — lib/ime.ts cancels it).
    fireEvent.compositionEnd(input);
    const dispatched = fireEvent.keyDown(input, { key: 'Enter', keyCode: 13 });
    expect(dispatched).toBe(false);
    expect(openConfig).not.toHaveBeenCalled();
    expect(useStore.getState().paletteOpen).toBe(true);
  });
});
