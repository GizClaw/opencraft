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

describe('CommandPalette', () => {
  it('moves focus into the search field when the shortcut opens it', () => {
    // The app mounts the palette closed and opens it with ⌘K, so the
    // caret has to land in the commit that brings the panel up.
    useStore.setState({ paletteOpen: false });
    render(<CommandPalette />);
    expect(screen.queryByRole('combobox')).toBeNull();

    act(() => useStore.setState({ paletteOpen: true }));
    expect(screen.getByRole('combobox')).toHaveFocus();
  });

  it('lists commands and filters them as the query changes', async () => {
    render(<CommandPalette />);
    const input = screen.getByRole('combobox');

    expect(screen.getByText('Usage')).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'beta' } });
    await waitFor(() => expect(screen.queryByText('Usage')).toBeNull());
    expect(screen.getByText('Beta')).toBeInTheDocument();
  });

  it('runs the highlighted command on Enter', () => {
    const openConfig = vi.fn();
    useStore.setState({ openConfig });
    render(<CommandPalette />);

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
    render(<CommandPalette />);

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
    render(<CommandPalette />);
    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: 'zzzz' },
    });
    await waitFor(() =>
      expect(screen.getByText('Nothing matches')).toBeInTheDocument(),
    );
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('closes when the overlay dismisses, and renders nothing while closed', async () => {
    const { container } = render(<CommandPalette />);
    expect(screen.getByRole('combobox')).toBeInTheDocument();

    useStore.setState({ paletteOpen: false });
    await waitFor(() =>
      expect(container.querySelector('[role="dialog"]')).toBeNull(),
    );
  });
});
