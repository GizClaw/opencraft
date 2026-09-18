import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ToolView } from '../lib/store';
import { ApplyPatchView, ToolCard } from './ToolCard';

const apiMock = vi.hoisted(() => ({
  renderPatch: vi.fn(),
  renderSkillPatch: vi.fn(),
  readPreview: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  apiMock.renderPatch.mockReset();
  apiMock.renderSkillPatch.mockReset();
  apiMock.renderSkillPatch.mockResolvedValue([]);
  apiMock.readPreview.mockReset();
  // Stand-in for the workspace diff renderer: only codex patch text
  // parses, anything else is rejected like the Go binding does.
  apiMock.renderPatch.mockImplementation(async (patch: string) => {
    if (!patch.startsWith('*** Begin Patch')) {
      const first = patch.split('\n')[0];
      throw new Error(`apply_patch: unexpected line outside patch: "${first}"`);
    }
    return [];
  });
});

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
  it('renders generated media as thumbnails, a player, and previews', async () => {
    const user = userEvent.setup();
    apiMock.readPreview.mockImplementation(async (path: string) =>
      path.endsWith('.mp4')
        ? {
            path,
            rel: path,
            root: 'workspace',
            name: 'clip.mp4',
            size: 3,
            media_type: 'video/mp4',
            kind: 'video',
            stream_url: `http://127.0.0.1:1/media/token/${path}`,
          }
        : {
            path,
            rel: path,
            root: 'workspace',
            name: path.split('/').pop() ?? path,
            size: 3,
            media_type: 'image/png',
            kind: 'image',
            data_url: 'data:image/png;base64,AAA',
          },
    );
    render(
      <ToolCard
        tool={tool({
          name: 'generate_image',
          args: '{"prompt":"a red fox"}',
          result: JSON.stringify({
            paths: ['generated/image-a.png', 'generated/clip.mp4'],
            previews: ['generated/previews/preview-a.png'],
            count: 2,
            model: 'openai/gpt-image-2',
            hint: 'Images are workspace-relative',
          }),
        })}
      />,
    );
    await user.click(screen.getByRole('button', { name: /a red fox/i }));
    await waitFor(() => expect(apiMock.readPreview).toHaveBeenCalledTimes(3));
    await waitFor(() => {
      const video = document.querySelector('video');
      expect(video?.getAttribute('src')).toBe(
        'http://127.0.0.1:1/media/token/generated/clip.mp4',
      );
    });
    // Two images (the final one plus the streamed preview) and one
    // clickable path line under the player.
    expect(document.querySelectorAll('img')).toHaveLength(2);
    expect(screen.getByText('Previews')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'generated/clip.mp4' }),
    ).toBeInTheDocument();
  });

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

  it('shows the command when the model used the cmd alias', () => {
    render(
      <ToolCard
        tool={tool({
          args: '{"cmd":"git status --short"}',
          result: '{"exit_code":0,"stdout":"","stderr":""}',
        })}
      />,
    );
    expect(screen.getByText('git status --short')).toBeInTheDocument();
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
    // list_dir renders the result tree directly without the generic
    // arguments/result section labels.
    expect(screen.queryByText('arguments')).not.toBeInTheDocument();
    expect(screen.queryByText('参数')).not.toBeInTheDocument();
    expect(screen.queryByText('result')).not.toBeInTheDocument();
    expect(screen.queryByText('结果')).not.toBeInTheDocument();
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

describe('ApplyPatchView', () => {
  it('renders the decoded patch through the backend diff renderer', async () => {
    const patch =
      '*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch\n';
    apiMock.renderPatch.mockResolvedValueOnce([
      {
        path: 'a.txt',
        action: 'update',
        added: 1,
        removed: 1,
        lines: [
          { kind: 'delete', old_num: 3, new_num: 0, text: 'old' },
          { kind: 'add', old_num: 0, new_num: 3, text: 'new' },
        ],
      },
    ]);
    render(
      <ApplyPatchView
        tool={tool({ name: 'apply_patch', args: JSON.stringify({ patch }) })}
      />,
    );
    expect(apiMock.renderPatch).toHaveBeenCalledTimes(1);
    expect(apiMock.renderPatch).toHaveBeenCalledWith(patch);
    expect(await screen.findByText('new')).toBeInTheDocument();
  });

  it('keeps the raw arguments local when the call carries no patch text', async () => {
    // Models trained on other apply_patch harnesses send the patch as
    // "input". That text must never reach the backend parser: the call
    // fails and the desktop runtime logs the binding ERR this test
    // keeps out of the log.
    const args = JSON.stringify(
      { input: '*** Begin Patch\n*** End Patch\n' },
      null,
      2,
    );
    render(
      <ApplyPatchView
        tool={tool({ name: 'apply_patch', args, status: 'error' })}
      />,
    );
    expect(await screen.findByText('{')).toBeInTheDocument();
    expect(apiMock.renderPatch).not.toHaveBeenCalled();
  });

  it('skips the renderer when the arguments are not parseable JSON', async () => {
    render(
      <ApplyPatchView
        tool={tool({
          name: 'apply_patch',
          args: '{"patch":"*** Begin Patch',
          status: 'running',
        })}
      />,
    );
    expect(
      await screen.findByText('{"patch":"*** Begin Patch'),
    ).toBeInTheDocument();
    expect(apiMock.renderPatch).not.toHaveBeenCalled();
  });
});
