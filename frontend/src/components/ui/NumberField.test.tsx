import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NumberField, type NumberFieldProps } from './NumberField';

// The field's contract covers what a browser will not do for us: when a
// value is clamped, and what an empty field means. Typing is never
// clamped — "15" on the way to "150" is not the app's to correct — so the
// bounds are the blur's business; leaving a cleared field restores the
// last committed value (a cancelled edit, not a zero), unless the call
// site declared an empty state, in which case the empty field survives.
//
// The tests drive the field the way the app does: a controlled harness
// that feeds every commit back as the value prop.
function Harness({
  initial = 5,
  onCommit,
  ...props
}: Omit<NumberFieldProps, 'value' | 'onChange'> & {
  initial?: number | '';
  onCommit?: (value: number | '') => void;
}) {
  const [value, setValue] = useState<number | ''>(initial);
  return (
    <NumberField
      {...props}
      value={value}
      onChange={(next) => {
        setValue(next);
        onCommit?.(next);
      }}
    />
  );
}

const noop = () => {};

describe('NumberField', () => {
  it('writes the digits as they are typed and clamps on the way out', async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness initial={5} min={1} max={10} onCommit={onCommit} />);
    const field = screen.getByRole('spinbutton');

    await user.clear(field);
    await user.type(field, '150');
    expect(field).toHaveValue(150);
    expect(onCommit).toHaveBeenLastCalledWith(150);

    await user.tab();
    expect(onCommit).toHaveBeenLastCalledWith(10);
    expect(field).toHaveValue(10);
  });

  it('treats a cleared field as a cancelled edit, not a zero', async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness initial={5} min={1} max={10} onCommit={onCommit} />);
    const field = screen.getByRole('spinbutton');

    await user.clear(field);
    expect(onCommit).not.toHaveBeenCalled();
    await user.tab();
    // Number('') || 0 used to write a zero nobody typed.
    expect(onCommit).not.toHaveBeenCalled();
    expect(field).toHaveValue(5);
  });

  it('keeps the empty state where the call site declared one', async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(
      <Harness
        initial={2048}
        min={256}
        max={16384}
        allowEmpty
        onCommit={onCommit}
      />,
    );
    const field = screen.getByRole('spinbutton');

    await user.clear(field);
    // An optional limit with a provider default: empty is a value, so it
    // commits and stays committed.
    expect(onCommit).toHaveBeenLastCalledWith('');
    await user.tab();
    expect(onCommit).toHaveBeenLastCalledWith('');
    expect(field).toHaveValue(null);
  });

  it('steps from the committed value and stops at the bounds', async () => {
    const user = userEvent.setup();
    render(
      <Harness initial={4} min={1} max={5} steppers label="Concurrency" />,
    );
    const field = screen.getByRole('spinbutton', { name: 'Concurrency' });
    const up = screen.getByRole('button', { name: 'Increase Concurrency' });
    const down = screen.getByRole('button', { name: 'Decrease Concurrency' });

    await user.click(up);
    expect(field).toHaveValue(5);
    // The top of the range is the button's business, not a click the
    // handler has to reject.
    expect(up).toBeDisabled();
    expect(down).toBeEnabled();

    await user.click(down);
    expect(field).toHaveValue(4);
    expect(up).toBeEnabled();
  });

  it('stays a native number input so the browser keeps the keys', () => {
    render(
      <NumberField
        value={5}
        onChange={noop}
        min={1}
        max={10}
        step={2}
        label="Retries"
        unit="tries"
      />,
    );
    const field = screen.getByRole('spinbutton', { name: 'Retries' });
    // min/max/step, the arrow keys and the value hygiene are the input's
    // own; the field owns the frame, not the arithmetic.
    expect(field).toHaveAttribute('min', '1');
    expect(field).toHaveAttribute('max', '10');
    expect(field).toHaveAttribute('step', '2');
    // The unit is a label in its own cell, never a second field.
    expect(screen.getByText('tries')).toBeInTheDocument();
    expect(screen.getAllByRole('spinbutton')).toHaveLength(1);
  });
});
