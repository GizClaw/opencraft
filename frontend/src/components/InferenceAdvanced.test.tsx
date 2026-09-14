import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { AdvancedSection, KeyValueEditor } from './InferenceAdvanced';
import type { ProviderAdvanced } from '../lib/types';

function renderSection(driver: string, advanced: ProviderAdvanced = {}) {
  const onUpdate = vi.fn();
  render(
    <AdvancedSection
      row={{ advanced, api: 'responses' }}
      driver={driver}
      onUpdate={onUpdate}
    />,
  );
  return onUpdate;
}

describe('AdvancedSection', () => {
  it('groups the OpenAI wire knobs by the driver sections', () => {
    renderSection('openai');
    expect(screen.getByText('Endpoint')).toBeInTheDocument();
    expect(screen.getByText('Auth')).toBeInTheDocument();
    expect(screen.getByText('Transport')).toBeInTheDocument();
    expect(screen.getByText('Wire dialect')).toBeInTheDocument();
    expect(screen.getByText('Request body')).toBeInTheDocument();
    expect(screen.getByText('Routing')).toBeInTheDocument();
    // The responses surface has no chat streaming knobs and no video
    // extension; both belong to the chat surface only.
    expect(screen.queryByText('Chat streaming')).toBeNull();
    expect(screen.queryByText(/Accept video blocks/)).toBeNull();
  });

  it('shows the chat surface knobs and hides the responses-only ones', () => {
    render(
      <AdvancedSection
        row={{ advanced: {}, api: 'chat' }}
        driver="openai"
        onUpdate={vi.fn()}
      />,
    );
    expect(screen.getByText('Chat streaming')).toBeInTheDocument();
    expect(screen.getByText('Chat: include usage')).toBeInTheDocument();
    expect(screen.getByText(/Accept video blocks/)).toBeInTheDocument();
    expect(screen.queryByText('Reasoning summary')).toBeNull();
    expect(screen.queryByText('Truncation')).toBeNull();
  });

  it('shows the video toggle on a compatible Messages endpoint', () => {
    renderSection('anthropic');
    expect(screen.getByText(/Accept video blocks/)).toBeInTheDocument();
    expect(screen.queryByText('Routing')).toBeNull();
  });

  it('reports the retention policy as one of its four values', () => {
    const onUpdate = renderSection('openai');
    fireEvent.click(screen.getByRole('button', { name: 'Store responses' }));
    fireEvent.click(screen.getByRole('option', { name: 'Omit the field' }));
    expect(onUpdate).toHaveBeenCalledWith('store', 'omit');
    fireEvent.click(screen.getByRole('button', { name: 'Store responses' }));
    fireEvent.click(screen.getByRole('option', { name: 'default' }));
    expect(onUpdate).toHaveBeenLastCalledWith('store', '');
  });

  it('renders read-only for a plugin-owned deployment', () => {
    render(
      <AdvancedSection
        row={{ advanced: { timeout: '90s' }, api: 'responses' }}
        driver="openai"
        disabled
        onUpdate={vi.fn()}
      />,
    );
    expect(screen.getByRole('textbox', { name: 'Timeout' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Store responses' }),
    ).toBeDisabled();
    expect(screen.getByText('owned by a plugin')).toBeInTheDocument();
    // The map editors lose their add affordance too.
    expect(screen.queryByText('Add entry')).toBeNull();
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
