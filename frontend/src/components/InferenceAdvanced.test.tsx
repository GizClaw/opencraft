import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { AdvancedSection, KeyValueEditor } from './InferenceAdvanced';
import type { ProviderAdvanced } from '../lib/types';

function renderSection(driver: string, advanced: ProviderAdvanced = {}) {
  const onUpdate = vi.fn();
  render(
    <AdvancedSection row={{ advanced }} driver={driver} onUpdate={onUpdate} />,
  );
  return onUpdate;
}

describe('AdvancedSection', () => {
  it('shows the OpenAI wire knobs and the video toggle', () => {
    renderSection('openai');
    expect(screen.getByText('Routing')).toBeInTheDocument();
    expect(screen.getByText('Chat: include usage')).toBeInTheDocument();
    expect(screen.getByText(/Accept video blocks/)).toBeInTheDocument();
  });

  it('shows the video toggle on a compatible Messages endpoint', () => {
    renderSection('anthropic');
    expect(screen.getByText(/Accept video blocks/)).toBeInTheDocument();
    expect(screen.queryByText('Routing')).toBeNull();
  });

  it('reports the retention policy as one of its four values', () => {
    const onUpdate = renderSection('openai');
    const select = screen.getByRole('combobox', { name: 'Store responses' });
    fireEvent.change(select, { target: { value: 'omit' } });
    expect(onUpdate).toHaveBeenCalledWith('store', 'omit');
    fireEvent.change(select, { target: { value: '' } });
    expect(onUpdate).toHaveBeenLastCalledWith('store', '');
  });

  it('clears a text field by reporting an empty string', () => {
    const onUpdate = renderSection('openai', { timeout: '90s' });
    fireEvent.change(screen.getByDisplayValue('90s'), {
      target: { value: '' },
    });
    expect(onUpdate).toHaveBeenCalledWith('timeout', '');
  });
});

describe('KeyValueEditor', () => {
  it('edits existing entries and reports the committed record', () => {
    const onChange = vi.fn();
    render(
      <KeyValueEditor
        label="Query params"
        value={{ 'api-version': '2025-04-01-preview' }}
        onChange={onChange}
      />,
    );
    fireEvent.change(screen.getByDisplayValue('api-version'), {
      target: { value: 'api_version' },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      api_version: '2025-04-01-preview',
    });
  });

  it('adds a row and drops entries with a blank key or value', () => {
    const onChange = vi.fn();
    render(<KeyValueEditor label="Headers" onChange={onChange} />);
    fireEvent.click(screen.getByText('+ add'));
    fireEvent.change(screen.getByLabelText('Headers key 1'), {
      target: { value: 'x-gateway' },
    });
    fireEvent.change(screen.getByLabelText('Headers value 1'), {
      target: { value: 'one' },
    });
    expect(onChange).toHaveBeenLastCalledWith({ 'x-gateway': 'one' });

    fireEvent.change(screen.getByLabelText('Headers value 1'), {
      target: { value: '' },
    });
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  it('removes a row', () => {
    const onChange = vi.fn();
    render(
      <KeyValueEditor
        label="Headers"
        value={{ a: '1', b: '2' }}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByLabelText('Headers remove 1'));
    expect(onChange).toHaveBeenLastCalledWith({ b: '2' });
  });
});
