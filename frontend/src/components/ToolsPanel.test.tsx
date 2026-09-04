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
  it('opens a server in the detail dialog, edits it, and applies with Save & apply', async () => {
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

    expect(await screen.findByText('git')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();

    await user.click(screen.getByText('git'));

    const nameInput = screen.getByLabelText('Server name');
    expect(nameInput).toHaveValue('git');
    await user.clear(nameInput);
    await user.type(nameInput, 'git2');
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));

    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({
        name: 'git2',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      }),
    ]);
    expect(await screen.findByText('Connecting')).toBeInTheDocument();
    expect(apiMock.mcpStatus).toHaveBeenCalledTimes(1);
  });

  it('filters configured servers and adds a new server through the dialog', async () => {
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      },
      {
        name: 'fetch',
        transport: 'stdio',
        command: 'uvx',
        args: ['mcp-server-fetch'],
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([]);
    const user = userEvent.setup();

    render(<MCPSection />);

    await screen.findByText('git');
    const search = screen.getByRole('textbox', {
      name: 'Search configured servers…',
    });
    await user.type(search, 'fetch');
    expect(screen.getByText('fetch')).toBeInTheDocument();
    expect(screen.queryByText('git')).not.toBeInTheDocument();

    await user.clear(search);
    await user.click(screen.getByRole('button', { name: 'Add server' }));
    await user.type(screen.getByLabelText('Server name'), 'memory');
    await user.type(screen.getByLabelText('Command'), 'npx');
    await user.type(screen.getByLabelText('Arguments'), 'mcp-server-memory');
    await user.click(screen.getByRole('button', { name: 'Done' }));

    expect(screen.getByText('memory')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));
    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({ name: 'git', command: 'npx' }),
      expect.objectContaining({ name: 'fetch', command: 'uvx' }),
      expect.objectContaining({
        name: 'memory',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-memory'],
      }),
    ]);
  });

  it('switches transport from the styled dropdown', async () => {
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([]);
    apiMock.saveMCP.mockResolvedValue(undefined);
    const user = userEvent.setup();

    render(<MCPSection />);

    await screen.findByText('git');
    await user.click(screen.getByText('git'));
    await user.click(screen.getByLabelText('Transport'));
    await user.click(screen.getByRole('button', { name: 'http' }));

    expect(screen.getByLabelText('URL')).toBeInTheDocument();
    await user.type(screen.getByLabelText('URL'), 'https://mcp.example.com');
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));

    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({
        name: 'git',
        transport: 'http',
        url: 'https://mcp.example.com',
      }),
    ]);
  });

  it('adds environment variables as key/value rows', async () => {
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([]);
    apiMock.saveMCP.mockResolvedValue(undefined);
    const user = userEvent.setup();

    render(<MCPSection />);

    await screen.findByText('git');
    await user.click(screen.getByText('git'));
    await user.click(screen.getByRole('button', { name: 'Add variable' }));
    await user.type(screen.getByPlaceholderText('KEY'), 'FOO');
    await user.type(screen.getByPlaceholderText('VALUE'), 'bar');
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));

    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        env: { FOO: 'bar' },
      }),
    ]);
  });

  it('removes a server through the row menu', async () => {
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([]);
    apiMock.saveMCP.mockResolvedValue(undefined);
    const user = userEvent.setup();

    render(<MCPSection />);

    await screen.findByText('git');
    const moreButton = screen.getByRole('button', { name: 'More actions' });
    await user.click(moreButton);
    expect(
      screen.getByRole('button', { name: 'Remove server' }),
    ).toBeInTheDocument();

    await user.click(moreButton);
    expect(
      screen.queryByRole('button', { name: 'Remove server' }),
    ).not.toBeInTheDocument();

    await user.click(moreButton);
    await user.click(screen.getByRole('button', { name: 'Remove server' }));

    expect(screen.queryByText('git')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));
    expect(apiMock.saveMCP).toHaveBeenCalledWith([]);
  });
});
