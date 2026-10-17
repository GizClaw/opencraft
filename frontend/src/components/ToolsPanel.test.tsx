import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  envMapFromText,
  formatEnvText,
  MCPSection,
  parseEnvText,
} from './ToolsPanel';

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

  it('stores an env value with its whitespace intact', async () => {
    // The rows are the only place env values are edited, so what a value
    // looks like on screen is what has to reach the payload: the save
    // path used to trim it.
    apiMock.mcpConfig.mockResolvedValue([
      {
        name: 'git',
        transport: 'stdio',
        command: 'npx',
        args: ['mcp-server-git'],
        env: { TOKEN: ' Basic abc==' },
      },
    ]);
    apiMock.mcpStatus.mockResolvedValue([]);
    apiMock.saveMCP.mockResolvedValue(undefined);
    const user = userEvent.setup();

    render(<MCPSection />);
    await screen.findByText('git');
    await user.click(screen.getByText('git'));
    // toHaveValue compares the input's value verbatim: the display-value
    // query normalizes whitespace and could not tell the two apart.
    const value = await screen.findByPlaceholderText('VALUE');
    expect(value).toHaveValue(' Basic abc==');
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));

    expect(apiMock.saveMCP).toHaveBeenCalledWith([
      expect.objectContaining({
        name: 'git',
        env: { TOKEN: ' Basic abc==' },
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
      screen.getByRole('menuitem', { name: 'Remove server' }),
    ).toBeInTheDocument();

    await user.click(moreButton);
    // The panel animates out, so it leaves the DOM one exit-frame after
    // the menu closes rather than on the next render.
    await waitFor(() =>
      expect(
        screen.queryByRole('menuitem', { name: 'Remove server' }),
      ).not.toBeInTheDocument(),
    );

    await user.click(moreButton);
    await user.click(screen.getByRole('menuitem', { name: 'Remove server' }));

    expect(screen.queryByText('git')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save & apply' }));
    expect(apiMock.saveMCP).toHaveBeenCalledWith([]);
  });
});

// The env text is read in two places — the dialog's rows and the payload
// — and they used to disagree: a bare name looked like a key with an
// empty value in the form and was dropped by the save.
describe('MCP env text', () => {
  it('reads a line without "=" as a key with an empty value', () => {
    expect(parseEnvText('FOO=bar\nBAZ')).toEqual([
      { key: 'FOO', value: 'bar' },
      { key: 'BAZ', value: '' },
    ]);
    // The same parse feeds the payload, so the row the form shows is the
    // row that gets saved.
    expect(envMapFromText('FOO=bar\nBAZ')).toEqual({ FOO: 'bar', BAZ: '' });
  });

  it('needs a name for a key and keeps the value as typed', () => {
    // A line starting with '=' names nothing and a blank line is only the
    // empty row the "add" button round-trips, so neither is a variable.
    // The value of a real one is stored as it stands.
    expect(envMapFromText('FOO= bar \n=orphan')).toEqual({ FOO: ' bar ' });
    expect(envMapFromText('FOO=bar\n\n')).toEqual({ FOO: 'bar' });
  });

  it('round-trips the rows through the text it read them from', () => {
    expect(formatEnvText(parseEnvText('A=1\nB='))).toBe('A=1\nB=');
    // A bare name is the same row with an empty value, so writing the
    // rows back spells it the one way the payload reads it.
    expect(formatEnvText(parseEnvText('A=1\nB'))).toBe('A=1\nB=');
  });
});
