import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { ToolView } from '../lib/store';
import { ToolCard } from './ToolCard';

function tool(overrides: Partial<ToolView>): ToolView {
  return {
    id: 't-1',
    name: 'exec_command',
    args: '{"command":"ls"}',
    status: 'done',
    ...overrides,
  };
}

describe('ToolCard', () => {
  it('renders exec_command with exit code and stdout', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          result: '{"exit_code":0,"stdout":"README.md\\n","stderr":""}',
        })}
      />,
    );
    expect(screen.getByText('ls')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /ls/i }));
    expect(screen.getAllByText('README.md').length).toBeGreaterThan(0);
  });

  it('renders read_file content', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'read_file',
          args: '{"file_path":"a.go"}',
          result:
            '{"file_path":"a.go","content":"package main\\n","total_lines":1}',
        })}
      />,
    );
    await user.click(screen.getByRole('button', { name: /a\.go/i }));
    expect(screen.getByText('package main')).toBeInTheDocument();
  });

  it('renders list_dir as a tree', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'list_dir',
          args: '{"path":"src"}',
          result:
            '{"path":"src","entries":[{"path":"src/a.go","type":"file","size":10}]}',
        })}
      />,
    );
    await user.click(screen.getByRole('button', { name: /list/i }));
    expect(screen.getByText('a.go')).toBeInTheDocument();
  });

  it('shows a failed exec result', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          result: '{"exit_code":2,"stdout":"","stderr":"boom"}',
        })}
      />,
    );
    expect(screen.getByText(/exit\s+2/i)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /ls/i }));
    expect(screen.getAllByText('boom').length).toBeGreaterThan(0);
  });
});
