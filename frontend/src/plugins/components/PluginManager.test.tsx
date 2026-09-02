import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { usePluginStore } from '../store';
import type { PluginSummary } from '../types';
import { PluginManager } from './PluginManager';

const apiMock = vi.hoisted(() => ({
  pluginTools: vi.fn(),
  pluginList: vi.fn(),
}));

vi.mock('../../lib/api', () => ({ api: apiMock }));

const toolPlugin: PluginSummary = {
  id: 'tool-plugin',
  name: 'Tool Plugin',
  version: '1.0.0',
  entry: 'dist/index.js',
  permissions: ['tools:expose'],
  enabled: true,
  hasTools: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  usePluginStore.setState({
    plugins: [toolPlugin],
    panels: [],
    entries: [],
    commands: [],
    statusBar: [],
    errors: {},
    loading: false,
  });
  apiMock.pluginTools.mockResolvedValue([
    {
      name: 'do_thing',
      description: 'Does a thing',
      method: 'thing.run',
      mutates_state: false,
    },
    {
      name: 'mutate_thing',
      method: 'thing.mutate',
      mutates_state: true,
    },
  ]);
});

describe('PluginManager tools visibility', () => {
  it('lists plugin agent tools on expand', async () => {
    const user = userEvent.setup();
    render(<PluginManager showTitle={false} />);

    await user.click(screen.getByRole('button', { name: 'Show agent tools' }));
    expect(apiMock.pluginTools).toHaveBeenCalledWith('tool-plugin');
    expect(await screen.findByText('do_thing')).toBeInTheDocument();
    expect(screen.getByText('Does a thing')).toBeInTheDocument();
    expect(screen.getByText('mutate_thing')).toBeInTheDocument();
    expect(screen.getByText('mutates')).toBeInTheDocument();
    expect(screen.getByText('thing.run')).toBeInTheDocument();
  });
});
