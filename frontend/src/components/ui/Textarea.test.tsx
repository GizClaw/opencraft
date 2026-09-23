import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Input } from './Input';
import { Textarea } from './Textarea';

const noop = () => {};

describe('Textarea', () => {
  it('reads its size, surface and error state off the same ladder as Input', () => {
    // Two fields of one form: the rungs are the contract between them, so
    // a compact raised invalid field looks like a compact raised invalid
    // field whatever its shape.
    render(
      <>
        <Input aria-label="Text" size="sm" surface="raised" invalid />
        <Textarea aria-label="Multi" size="sm" surface="raised" invalid />
      </>,
    );
    const input = screen.getByRole('textbox', { name: 'Text' });
    const textarea = screen.getByRole('textbox', { name: 'Multi' });
    for (const rung of ['text-xs', 'bg-panel2', 'border-err']) {
      expect(input.className).toContain(rung);
      expect(textarea.className).toContain(rung);
    }
  });

  it('frames a field, unless it already sits inside a box', () => {
    const { rerender } = render(
      <Textarea aria-label="Paths" value="/a" onChange={noop} />,
    );
    expect(screen.getByRole('textbox', { name: 'Paths' }).className).toContain(
      'border-edge',
    );
    // The frame would be a second border inside the bordered box.
    rerender(
      <Textarea aria-label="Paths" seamless value="/a" onChange={noop} />,
    );
    const bare = screen.getByRole('textbox', { name: 'Paths' }).className;
    expect(bare).toContain('bg-transparent');
    expect(bare).not.toContain('border');
    expect(bare).not.toContain('rounded');
  });

  it('grows downwards only, and keeps its face on request', () => {
    render(
      <Textarea
        aria-label="Config"
        mono
        rows={2}
        value={'{\n}'}
        onChange={noop}
      />,
    );
    const field = screen.getByRole('textbox', { name: 'Config' });
    expect(field).toHaveValue('{\n}');
    expect(field).toHaveAttribute('rows', '2');
    // A field dragged wider than the column it sits in is a layout the
    // user has to undo.
    expect(field.className).toContain('resize-y');
    expect(field.className).not.toContain('resize-x');
    expect(field.className).toContain('font-mono');
  });
});
