import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    inferenceCatalog: vi.fn(async () => ({
      version: 'test',
      templates: [
        {
          id: 'openai-official',
          label: 'OpenAI',
          type: 'openai',
          vendor: 'OpenAI',
          api: 'responses',
          models: [
            {
              name: 'gpt-5.6-sol',
              kind: 'generate',
              capabilities: { outputs: ['text'], hosted_web_search: true },
            },
          ],
        },
        {
          id: 'zhipu-glm-video',
          label: 'Zhipu GLM (video in)',
          type: 'openai',
          vendor: 'Zhipu AI',
          api: 'chat',
          advanced: { video_input: true },
          models: [
            {
              name: 'glm-5.3-flash',
              kind: 'generate',
              capabilities: {
                inputs: ['text', 'image', 'video'],
                outputs: ['text'],
              },
            },
          ],
        },
      ],
      models: [
        {
          id: 'openai/gpt-5.6-sol',
          type: 'openai',
          vendor: 'OpenAI',
          label: 'GPT-5.6 Sol',
          model: {
            name: 'gpt-5.6-sol',
            kind: 'generate',
            capabilities: { outputs: ['text'], hosted_web_search: true },
          },
        },
        {
          id: 'zhipu/glm-5.3-flash',
          type: 'openai',
          vendor: 'Zhipu AI',
          label: 'GLM-5.3-Flash',
          model: {
            name: 'glm-5.3-flash',
            kind: 'generate',
            capabilities: {
              inputs: ['text', 'image', 'video'],
              outputs: ['text'],
            },
          },
        },
      ],
    })),
    saveInstances: vi.fn(async () => undefined),
    modelUsage: vi.fn(async () => [
      {
        model: 'deepseek-v4-flash',
        total_tokens: 161989893,
        input_tokens: 160206252,
        output_tokens: 1783641,
        cache_read_tokens: 156851328,
        cache_write_tokens: 0,
        reasoning_tokens: 1251298,
        latency_ms: 14119381,
        calls: 1910,
        workspaces: 3,
        sessions: 13,
        updated_at: '2026-09-10T06:21:19Z',
      },
      {
        model: 'deepseek-flash',
        total_tokens: 2392488,
        input_tokens: 2358509,
        output_tokens: 33979,
        cache_read_tokens: 2292608,
        cache_write_tokens: 0,
        reasoning_tokens: 24114,
        latency_ms: 169731,
        calls: 37,
        workspaces: 1,
        sessions: 2,
        updated_at: '2026-09-15T03:47:36Z',
      },
    ]),
    modelUsageSessionCount: vi.fn(async () => 14),
    modelUsageSeries: vi.fn(async () => [
      {
        time: '2026-09-15T03:00:00Z',
        input_tokens: 2349504,
        output_tokens: 33326,
        cache_read_tokens: 2283904,
        cache_write_tokens: 0,
        reasoning_tokens: 23799,
      },
    ]),
    telemetryExport: vi.fn(async () => ({
      enabled: true,
      configured: true,
      endpoint: 'collector.example:4318',
      insecure: false,
      headerNames: ['Authorization'],
      owner: 'sso-plugin',
    })),
    readLog: vi.fn(async () => ''),
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

  it('adds a driver instance at the top of the priority list', async () => {
    render(<ConfigPage />);
    await screen.findByPlaceholderText('Model');
    await waitFor(() => {
      screen.getByText('Add instance').click();
    });
    await waitFor(() => {
      const inputs = screen.getAllByPlaceholderText('Model');
      expect(inputs).toHaveLength(2);
      // The blank row the button just created leads; the stored
      // instance keeps its order behind it.
      expect(inputs[0]).toHaveValue('');
      expect(inputs[1]).toHaveValue('deepseek-v4-flash');
    });
  });

  it('prefills a whole instance from a built-in template', async () => {
    render(<ConfigPage />);
    await screen.findByPlaceholderText('Model');
    // Templates are pills under the driver picker, separate from the
    // driver list; the pill names the models it starts with.
    const template = await screen.findByRole('button', {
      name: 'OpenAI: gpt-5.6-sol',
    });
    template.click();
    await waitFor(() => {
      const inputs = screen.getAllByPlaceholderText('Model');
      // A new deployment lands at the top of the priority list.
      expect(inputs[0]).toHaveValue('gpt-5.6-sol');
    });
  });

  it('filters the template pills', async () => {
    render(<ConfigPage />);
    await screen.findByPlaceholderText('Model');
    expect(
      await screen.findByRole('button', { name: 'OpenAI: gpt-5.6-sol' }),
    ).toBeInTheDocument();

    const search = screen.getByLabelText('Search templates');
    // The vendor matches too, so "zhipu" keeps that pill and drops the rest.
    fireEvent.change(search, { target: { value: 'zhipu' } });
    expect(
      screen.getByRole('button', {
        name: 'Zhipu GLM (video in): glm-5.3-flash',
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'OpenAI: gpt-5.6-sol' }),
    ).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: 'nothing-matches' } });
    expect(screen.getByText('No matching templates.')).toBeInTheDocument();
  });

  it('carries template provider knobs into the new row', async () => {
    render(<ConfigPage />);
    await screen.findByPlaceholderText('Model');
    // A template may pin a provider-level fact its model needs: the
    // video-input template states wire.video_input for its chat row.
    const template = await screen.findByRole('button', {
      name: 'Zhipu GLM (video in): glm-5.3-flash',
    });
    template.click();
    const summaries = await screen.findAllByText('Advanced');
    // Top of the list is the template row that was just added.
    summaries[0].click();
    await waitFor(() =>
      expect(
        screen.getByLabelText('Accept video blocks (compatible endpoints)'),
      ).toBeChecked(),
    );
  });

  it('changes only the model row and warns about the fact it needs', async () => {
    render(<ConfigPage />);
    // Wait for the stored instance to load: saving an empty form is
    // rejected before it reaches the binding.
    await screen.findByPlaceholderText('Model');
    // Save and read the submitted instance, so the assertion below covers
    // every provider-level field the row ships — not just the two the
    // catalog used to rewrite.
    const saveAndRead = async (n: number) => {
      await waitFor(() => {
        screen.getByText('Save & apply').click();
      });
      await waitFor(() =>
        expect(apiMock.saveInstances).toHaveBeenCalledTimes(n),
      );
      const calls = (apiMock.saveInstances as ReturnType<typeof vi.fn>).mock
        .calls;
      return calls[n - 1][0] as {
        instances: Array<Record<string, unknown>>;
      };
    };
    const providerFields = ({
      models: _models,
      ...rest
    }: Record<string, unknown>) => rest;
    const before = await saveAndRead(1);

    const trigger = screen.getByLabelText('Fill from built-in models');
    trigger.click();
    const entry = await screen.findByRole('button', {
      name: /^glm-5\.3-flash/,
    });
    entry.click();
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Model')).toHaveValue('glm-5.3-flash'),
    );

    // The model declares video input, which this row cannot serve: the
    // page says so and leaves the provider settings alone.
    await waitFor(() => {
      const warnings = useStore
        .getState()
        .toasts.filter((item) => item.kind === 'warning');
      expect(warnings).toHaveLength(1);
      expect(warnings[0].text).toContain('glm-5.3-flash');
      expect(warnings[0].text).toContain('API mode');
    });
    const summary = screen.getAllByText('Advanced')[0];
    summary.click();
    await waitFor(() =>
      expect(
        screen.queryByLabelText('Accept video blocks (compatible endpoints)'),
      ).not.toBeInTheDocument(),
    );

    const after = await saveAndRead(2);
    expect(providerFields(after.instances[0])).toEqual(
      providerFields(before.instances[0]),
    );
    expect((after.instances[0].models as Array<{ name: string }>)[0].name).toBe(
      'glm-5.3-flash',
    );
  });

  it('filters the built-in model list by name, label, or vendor', async () => {
    render(<ConfigPage />);
    const trigger = await screen.findByLabelText('Fill from built-in models');
    trigger.click();
    const search = await screen.findByLabelText('Search built-in models');
    fireEvent.change(search, { target: { value: 'glm' } });
    expect(
      await screen.findByRole('button', {
        name: /^glm-5\.3-flash/,
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^gpt-5\.6-sol/ }),
    ).not.toBeInTheDocument();
    // The vendor matches too, so "moonshot" finds Kimi models — and the
    // mock carries none, which exercises the empty state.
    fireEvent.change(search, { target: { value: 'moonshot' } });
    expect(await screen.findByText('No matching models.')).toBeInTheDocument();
  });

  it('fills a model row from the built-in model catalog', async () => {
    render(<ConfigPage />);
    const trigger = await screen.findByLabelText('Fill from built-in models');
    trigger.click();
    const entry = await screen.findByRole('button', { name: /GPT-5\.6 Sol/ });
    entry.click();
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Model')).toHaveValue('gpt-5.6-sol'),
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
    // panel is collapsed, so open it before reading the field.
    const summary = screen.getAllByText('Advanced')[0];
    summary.click();
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Reasoning channel' }),
      ).toHaveTextContent('Plain text'),
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

describe('ConfigPage usage', () => {
  it('defaults the trend to the all-models aggregate', async () => {
    useStore.setState({ configTab: 'usage' });
    render(<ConfigPage />);

    const picker = await screen.findByRole('button', { name: 'All models' });
    expect(picker).toBeInTheDocument();

    // The empty model string is what the backend aggregates on, so the
    // default request must not name a model.
    await waitFor(() =>
      expect(
        (apiMock.modelUsageSeries as ReturnType<typeof vi.fn>).mock.calls[0],
      ).toEqual([
        '',
        'day',
        expect.any(Number),
        expect.any(String),
        expect.any(String),
      ]),
    );
  });

  it('explains an empty range instead of drawing a blank plot', async () => {
    useStore.setState({ configTab: 'usage' });
    (
      apiMock.modelUsageSeries as ReturnType<typeof vi.fn>
    ).mockResolvedValueOnce([]);
    render(<ConfigPage />);

    expect(
      await screen.findByText('No usage recorded in this range.'),
    ).toBeInTheDocument();
  });
});

describe('ConfigPage diagnostics', () => {
  it('shows the OTLP export sink in the diagnostics tab', async () => {
    useStore.setState({ configTab: 'diagnostics' });
    render(<ConfigPage />);

    // Telemetry egress lives with the log viewer it feeds, not with the
    // plugin list that may point it somewhere else.
    const status = await screen.findByText(/configured by sso-plugin/);
    expect(status.textContent).toContain('collector.example:4318');
  });
});
