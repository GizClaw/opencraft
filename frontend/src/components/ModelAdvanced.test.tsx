import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ModelAdvanced, specJsonError } from './ModelAdvanced';

const active = {
  lifecycleStatus: '',
  lifecycleReplacementProvider: '',
  lifecycleReplacementName: '',
  lifecycleNotes: '',
  specJson: '',
};

describe('ModelAdvanced', () => {
  it('hides the replacement fields while the model is active', () => {
    render(<ModelAdvanced value={active} onUpdate={() => {}} />);
    expect(screen.getByText('Status')).toBeInTheDocument();
    expect(screen.queryByText('Replacement model')).toBeNull();
  });

  it('reports a deprecation with its replacement', () => {
    const onUpdate = vi.fn();
    render(
      <ModelAdvanced
        value={{ ...active, lifecycleStatus: 'deprecated' }}
        onUpdate={onUpdate}
      />,
    );
    fireEvent.change(screen.getByRole('combobox', { name: 'Status' }), {
      target: { value: 'retired' },
    });
    expect(onUpdate).toHaveBeenCalledWith({ lifecycleStatus: 'retired' });
    fireEvent.change(screen.getByLabelText('Replacement model'), {
      target: { value: 'gpt-5.6-terra' },
    });
    expect(onUpdate).toHaveBeenCalledWith({
      lifecycleReplacementName: 'gpt-5.6-terra',
    });
  });

  it('flags a driver-field blob that is not a JSON object', () => {
    expect(specJsonError('')).toBe(false);
    expect(specJsonError('{"wire_model":"MiniMax-H3"}')).toBe(false);
    expect(specJsonError('[1,2]')).toBe(true);
    expect(specJsonError('{oops')).toBe(true);
    render(
      <ModelAdvanced
        value={{ ...active, specJson: '{oops' }}
        onUpdate={() => {}}
      />,
    );
    expect(screen.getByText('must be a JSON object')).toBeInTheDocument();
  });
});
