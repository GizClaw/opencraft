import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MCPSection } from './ToolsPanel';

const apiMock = vi.hoisted(() => ({
  mcpConfig: vi.fn(),
  mcpStatus: vi.fn(),
  saveMCP: vi.fn(),
  testMCP: vi.fn(),
  openExternal: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('MCPSection', () => {
  it('shows connecting after save and lets polling refresh the status', async () => {
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([{ name: 'git', status: 'connected' }]);
    apiMock.saveMCP.mockResolvedValue(undefined);
    const user = userEvent.setup();

    render(<MCPSection />);

    expect(await screen.findByDisplayValue('git')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Save & apply' }));

    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      }),
    ]);
    expect(await screen.findByText('Connecting')).toBeInTheDocument();
    expect(apiMock.mcpStatus).toHaveBeenCalledTimes(1);
  });
});
