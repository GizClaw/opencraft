import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useStore } from '../lib/store';
import { ConfigPage } from './ConfigPage';

// The settings page loads every config surface on mount, so the mock
// answers anything it does not explicitly know with an empty result.
const apiMock = vi.hoisted(() => {
  const known: Record<string, unknown> = {
    providers: vi.fn(async () => [
      {
        id: 'openai',
        name: 'OpenAI',
        env_var: 'OPENAI_API_KEY',
        model_endpoint: false,
        impl: 'openai',
      },
      {
        id: 'anthropic',
        name: 'Anthropic',
        env_var: 'ANTHROPIC_API_KEY',
        model_endpoint: false,
        impl: 'anthropic',
      },
    ]),
    configState: vi.fn(async () => ({
      model: '',
      router: { max_attempts: 3, fallback_on_retry_exhausted: false },
      instances: [
        {
          stable_id: 'inst-aaa',
          type: 'openai',
          name: 'primary',
          api: 'responses',
          endpoint: 'https://gateway.example/v1',
          key_source: 'env',
          key_ref: '',
          key_set: true,
          key_env: true,
          key_keychain: false,
          models: [
            {
              name: 'deepseek-v4-flash',
              kind: 'generate',
              capabilities: {
                inputs: ['text'],
                outputs: ['text'],
                reasoning: { kind: 'toggle' },
              },
              endpoint: '',
            },
          ],
          advanced: { reasoning_channel: 'text' },
          enabled: true,
          managed: false,
        },
      ],
    })),
    saveInstances: vi.fn(async () => undefined),
  };
  return new Proxy(known, {
    get: (target, prop) =>
      prop in target ? target[prop as string] : vi.fn(async () => null),
  });
});

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
  useStore.setState({ configTab: 'inference' });
});

describe('ConfigPage inference', () => {
  it('defaults the add-instance picker to a real driver', async () => {
    render(<ConfigPage />);
    const picker = await screen.findByRole('button', {
      name: 'Inference driver',
    });
    expect(picker).toHaveTextContent('OpenAI');
    picker.click();
    expect(
      await screen.findByRole('button', { name: 'OpenAI' }),
    ).toBeInTheDocument();
  });

  it('starts an instance with one nameless model row', async () => {
    // Opencraft keeps no model table: an instance with no declared
    // models starts from one empty row for the deployment to fill in.
    (apiMock.configState as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      model: '',
      router: { max_attempts: 2, fallback_on_retry_exhausted: true },
      instances: [
        {
          stable_id: '',
          type: 'openai',
          name: '',
          api: 'responses',
          key_set: false,
          key_env: true,
          models: [],
          endpoint: '',
          advanced: {},
          enabled: true,
          managed: false,
        },
      ],
    });
    render(<ConfigPage />);
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Model')).toHaveValue(''),
    );
  });

  it('lists the drivers and saves the advanced knobs', async () => {
    render(<ConfigPage />);

    // The picker offers the drivers, not vendors.
    const picker = await screen.findByRole('button', {
      name: 'Inference driver',
    });
    expect(picker).toHaveTextContent('OpenAI');
    picker.click();
    expect(
      await screen.findByRole('button', { name: 'Anthropic' }),
    ).toBeInTheDocument();

    // The stored advanced knobs seed the form and survive an edit: the
    // section is collapsed, so open it before reading the field.
    const summary = screen.getAllByText('Advanced')[0];
    summary.click();
    await waitFor(() =>
      expect(screen.getByDisplayValue('text')).toBeInTheDocument(),
    );

    await waitFor(() => {
      const save = screen.getByText('Save & apply');
      save.click();
    });

    await waitFor(() => expect(apiMock.saveInstances).toHaveBeenCalledTimes(1));
    const payload = (apiMock.saveInstances as ReturnType<typeof vi.fn>).mock
      .calls[0][0];
    expect(payload.router).toEqual({
      max_attempts: 3,
      fallback_on_retry_exhausted: false,
    });
    expect(payload.instances[0].advanced).toEqual({
      reasoning_channel: 'text',
    });
    // The row round-trips in the canonical shape: one model declaration
    // with nested capabilities, and the credential restated as the env
    // source the form shows.
    expect(payload.instances[0].key_source).toBe('env');
    expect(payload.instances[0].models[0]).toEqual({
      name: 'deepseek-v4-flash',
      kind: 'generate',
      capabilities: {
        inputs: ['text'],
        outputs: ['text'],
        reasoning: { kind: 'toggle' },
        hosted_web_search: undefined,
      },
      endpoint: undefined,
      limits: {
        max_input_tokens: undefined,
        max_output_tokens: undefined,
      },
      lifecycle: undefined,
      driver_fields: undefined,
    });
  });
});
