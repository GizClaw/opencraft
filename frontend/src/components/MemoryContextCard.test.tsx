import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { MemoryContextCard } from './MemoryContextCard';
import type { MemorySettings } from '../lib/types';

// The card is presentational: the tab owns the state and the save call.
// What it has to get right is that no number is left unlabelled — every
// knob names its unit, and the two counts the fold adds up are spelled
// out as the window they produce.
const settings: MemorySettings = {
  max_raw_messages: 36,
  preserve_recent: 4,
  max_summary_bytes: 4096,
  replay_full_history: false,
};

const noop = () => {};

describe('MemoryContextCard', () => {
  it('labels every knob with its unit and states the window it adds up to', () => {
    render(
      <MemoryContextCard
        settings={settings}
        onChange={noop}
        saved={false}
        dirty={false}
        saving={false}
        error=""
        onSave={noop}
      />,
    );
    expect(screen.getByLabelText('Raw window')).toHaveValue(36);
    expect(screen.getByLabelText('Preserve recent')).toHaveValue(4);
    expect(screen.getByLabelText('Summary budget')).toHaveValue(4096);
    expect(screen.getAllByText('messages')).toHaveLength(2);
    expect(screen.getByText('bytes')).toBeInTheDocument();
    // raw + preserve is the window folding keeps verbatim.
    expect(
      screen.getByText(
        'Folding keeps the last 40 messages verbatim and replaces anything older with a summary.',
      ),
    ).toBeInTheDocument();
    // The byte budget reads in the unit people think in, next to the one
    // they type.
    expect(screen.getByText(/now 4\.0 KB/)).toBeInTheDocument();
  });

  it('reports each edit as a patch the tab can merge', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    // A controlled harness: the card's inputs are driven by the settings
    // it is handed, so a test that never feeds the patch back would type
    // into a field that keeps snapping to its prop.
    function Harness() {
      const [value, setValue] = useState(settings);
      return (
        <MemoryContextCard
          settings={value}
          onChange={(patch) => {
            onChange(patch);
            setValue((current) => ({ ...current, ...patch }));
          }}
          saved={false}
          dirty={false}
          saving={false}
          error=""
          onSave={noop}
        />
      );
    }
    render(<Harness />);
    const raw = screen.getByLabelText('Raw window');
    await user.clear(raw);
    await user.type(raw, '12');
    expect(onChange).toHaveBeenLastCalledWith({ max_raw_messages: 12 });
    await user.click(screen.getByLabelText('Full history replay'));
    expect(onChange).toHaveBeenLastCalledWith({ replay_full_history: true });
  });

  it('says which save bar has unsaved edits', () => {
    const { rerender } = render(
      <MemoryContextCard
        settings={settings}
        onChange={noop}
        saved={false}
        dirty={false}
        saving={false}
        error=""
        onSave={noop}
      />,
    );
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument();
    rerender(
      <MemoryContextCard
        settings={{ ...settings, preserve_recent: 8 }}
        onChange={noop}
        saved={false}
        dirty
        saving={false}
        error=""
        onSave={noop}
      />,
    );
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  });
});
