import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToolsSection } from './ToolsSection';

const apiMock = vi.hoisted(() => ({
  toolOptions: vi.fn(),
  saveToolOptions: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const state = {
  image: {
    instances: [
      {
        id: 'openai-img',
        label: 'OpenAI',
        impl: 'openai',
        managed: false,
        fields: [
          {
            name: 'background',
            kind: 'enum' as const,
            values: ['auto', 'opaque', 'transparent'],
          },
          {
            name: 'output_compression',
            kind: 'int' as const,
            min: 0,
            max: 100,
          },
        ],
        values: { background: 'transparent' },
      },
      {
        id: 'custom-img',
        label: 'custom (openai)',
        impl: 'custom',
        managed: true,
        fields: [],
        values: {},
      },
    ],
  },
  video: {
    instances: [
      {
        id: 'bytedance-vid',
        label: 'Bytedance',
        impl: 'bytedance',
        managed: false,
        fields: [{ name: 'camera_fixed', kind: 'bool' as const }],
        values: {},
      },
    ],
  },
};

describe('ToolsSection', () => {
  beforeEach(() => {
    apiMock.toolOptions.mockReset();
    apiMock.saveToolOptions.mockReset();
    apiMock.toolOptions.mockResolvedValue(state);
    apiMock.saveToolOptions.mockResolvedValue(undefined);
  });

  it('lists the tools as items and opens one in a dialog', async () => {
    const user = userEvent.setup();
    render(<ToolsSection />);
    expect(await screen.findByText('Image generation')).toBeInTheDocument();
    expect(screen.getByText('Video generation')).toBeInTheDocument();
    expect(screen.getByText('OpenAI · custom (openai)')).toBeInTheDocument();
    // Nothing but the item rows until one is opened.
    expect(screen.queryByLabelText('Background')).not.toBeInTheDocument();

    await user.click(screen.getByText('Image generation'));
    expect(
      screen.getByRole('dialog', { name: 'Image generation' }),
    ).toBeInTheDocument();
    expect(screen.getByText('OpenAI')).toBeInTheDocument();
    // The knob renders as the app's listbox pattern, showing the stored
    // value on the trigger.
    expect(screen.getByLabelText('Background')).toHaveTextContent(
      'transparent',
    );
    // A provider without a vocabulary says so instead of rendering an
    // empty control grid.
    expect(
      screen.getByText('This provider has no provider-specific options.'),
    ).toBeInTheDocument();
  });

  it('saves edits together with the other tool unchanged', async () => {
    const user = userEvent.setup();
    render(<ToolsSection />);
    await screen.findByText('Image generation');
    await user.click(screen.getByText('Image generation'));
    await user.click(screen.getByLabelText('Background'));
    await user.click(screen.getByRole('option', { name: 'opaque' }));
    await user.click(screen.getByRole('button', { name: /Save/i }));
    await waitFor(() =>
      expect(apiMock.saveToolOptions).toHaveBeenCalledTimes(1),
    );
    const req = apiMock.saveToolOptions.mock.calls[0][0] as {
      image: Record<string, Record<string, unknown>>;
      video: Record<string, Record<string, unknown>>;
    };
    expect(req.image['openai-img'].background).toBe('opaque');
    // The other tool travels along so its block is not cleared.
    expect(req.video).toEqual({ 'bytedance-vid': {} });
  });

  it('clears a knob back to the provider default', async () => {
    const user = userEvent.setup();
    render(<ToolsSection />);
    await screen.findByText('Image generation');
    await user.click(screen.getByText('Image generation'));
    await user.click(screen.getByLabelText('Background'));
    await user.click(screen.getByRole('option', { name: 'unset' }));
    await user.click(screen.getByRole('button', { name: /Save/i }));
    await waitFor(() =>
      expect(apiMock.saveToolOptions).toHaveBeenCalledTimes(1),
    );
    const req = apiMock.saveToolOptions.mock.calls[0][0] as {
      image: Record<string, Record<string, unknown>>;
    };
    expect(req.image['openai-img'].background).toBeUndefined();
  });

  it('surfaces a save failure', async () => {
    apiMock.saveToolOptions.mockRejectedValue(new Error('nope'));
    const user = userEvent.setup();
    render(<ToolsSection />);
    await screen.findByText('Image generation');
    await user.click(screen.getByText('Image generation'));
    await user.click(screen.getByRole('button', { name: /Save/i }));
    expect(await screen.findByText(/nope/)).toBeInTheDocument();
  });
});
