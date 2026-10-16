import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { PluginSummary } from '../types';
import { PluginInstallDialog } from './PluginInstallDialog';

const apiMock = vi.hoisted(() => ({
  pluginInspect: vi.fn(),
  pickFolder: vi.fn(),
  pickFile: vi.fn(),
  pluginInstall: vi.fn(),
  pluginInstallZip: vi.fn(),
}));

vi.mock('../../lib/api', () => ({ api: apiMock }));

function summary(over: Partial<PluginSummary> & { id: string }): PluginSummary {
  return {
    name: over.id,
    version: '1.0.0',
    entry: 'dist/index.js',
    permissions: [],
    enabled: true,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.pluginInspect.mockResolvedValue(summary({ id: 'plug' }));
});

// The path is inspected on the Go side (a filesystem probe), so typing
// or pasting a path must not fire one call per keystroke, and a slow
// answer for a path the user has already left must not decide what the
// dialog warns about.
describe('PluginInstallDialog path inspection', () => {
  it('inspects the settled path once', async () => {
    const user = userEvent.setup();
    render(<PluginInstallDialog onClose={() => {}} />);

    await user.type(
      screen.getByPlaceholderText('/path/to/plugin'),
      '/tmp/plug',
    );

    await waitFor(() => expect(apiMock.pluginInspect).toHaveBeenCalledTimes(1));
    expect(apiMock.pluginInspect).toHaveBeenCalledWith('/tmp/plug');
  });

  it('takes the newest answer and ignores a slower older one', async () => {
    const user = userEvent.setup();
    let resolveOld: (value: PluginSummary) => void = () => {};
    apiMock.pluginInspect
      .mockImplementationOnce(
        () =>
          new Promise<PluginSummary>((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockImplementationOnce(async () =>
        summary({
          id: 'newer',
          shadowsBuiltin: true,
          builtinVersion: '2.0.0',
        }),
      );
    render(<PluginInstallDialog onClose={() => {}} />);
    const input = screen.getByPlaceholderText('/path/to/plugin');

    await user.type(input, 'old-path');
    await waitFor(() => expect(apiMock.pluginInspect).toHaveBeenCalledTimes(1));

    await user.clear(input);
    await user.type(input, 'new-path');
    await waitFor(() => expect(apiMock.pluginInspect).toHaveBeenCalledTimes(2));
    expect(apiMock.pluginInspect).toHaveBeenLastCalledWith('new-path');
    expect(await screen.findByText(/v2\.0\.0/)).toBeInTheDocument();

    // The abandoned path answers after the path the user is looking at:
    // its warnings must not come back.
    resolveOld(
      summary({ id: 'older', shadowsBuiltin: true, builtinVersion: '1.0.0' }),
    );
    await waitFor(() => expect(apiMock.pluginInspect).toHaveBeenCalledTimes(2));
    expect(screen.queryByText(/v1\.0\.0/)).not.toBeInTheDocument();
    expect(screen.getByText(/v2\.0\.0/)).toBeInTheDocument();
  });
});
