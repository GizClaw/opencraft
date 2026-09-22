import { act, render, screen, waitFor } from '@testing-library/react';
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

  it('renders an exec_session read with the output it pulled', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: '{"action":"read","process_id":"dev","after_seq":0}',
          result: JSON.stringify({
            process_id: 'dev',
            chunks: [
              { seq: 0, stream: 'stdout', data: 'VITE ready in 240 ms\n' },
              { seq: 24, stream: 'stderr', data: 'warning: no lockfile\n' },
            ],
            next_seq: 48,
            eof: true,
          }),
        })}
      />,
    );
    // The collapsed header names the session and the action, peeks at
    // the newest output and carries the cursor of the read.
    // The accessible name concatenates the spans ("sessiondev· read…"),
    // so match on the pieces rather than on the visual spacing.
    const header = screen.getByRole('button', { name: /session.*dev.*read/ });
    expect(header).toHaveTextContent('VITE ready in 240 ms');
    expect(header).toHaveTextContent('#48');
    expect(header).toHaveTextContent('eof');
    await user.click(header);
    // Both streams of the read are rendered rather than swallowed.
    expect(screen.getByText('warning: no lockfile')).toBeInTheDocument();
    expect(screen.getAllByText('VITE ready in 240 ms').length).toBeGreaterThan(
      0,
    );
  });

  it('marks an exec_session read that pulled nothing new', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: '{"action":"read","process_id":"dev","after_seq":48}',
          result: '{"process_id":"dev","chunks":[],"next_seq":48,"eof":false}',
        })}
      />,
    );
    await user.click(
      screen.getByRole('button', { name: /session.*dev.*read/ }),
    );
    expect(screen.getByText('No new output')).toBeInTheDocument();
  });

  it('renders an exec_session start with its spec', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: JSON.stringify({
            action: 'start',
            process_id: 'dev',
            argv: ['npm', 'run', 'dev'],
            tty: true,
            rows: 40,
            cols: 120,
            workdir: '/srv/app',
          }),
          result: '{"process_id":"dev","started":true}',
        })}
      />,
    );
    // The start card shows the argv command line, not the tool name.
    await user.click(screen.getByRole('button', { name: /npm run dev/ }));
    expect(screen.getByText('/srv/app')).toBeInTheDocument();
    expect(screen.getByText('40×120')).toBeInTheDocument();
  });

  it('renders an exec_session wait with its exit code and reason', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: '{"action":"wait","process_id":"dev"}',
          result: '{"process_id":"dev","exit_code":1,"reason":"signaled"}',
        })}
      />,
    );
    const header = screen.getByRole('button', { name: /session.*dev.*wait/ });
    expect(header).toHaveTextContent('exit 1');
    expect(header).toHaveTextContent('signaled');
    await user.click(header);
    expect(screen.getByText('Reason:')).toBeInTheDocument();
    // The envelope JSON never leaks into the card.
    expect(screen.queryByText(/"exit_code"/)).not.toBeInTheDocument();
  });

  it('renders exec_session control actions as their ack', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: '{"action":"terminate","process_id":"dev"}',
          result: '{"terminated":true}',
        })}
      />,
    );
    await user.click(
      screen.getByRole('button', { name: /session.*dev.*terminate/ }),
    );
    expect(screen.getByText('Terminated')).toBeInTheDocument();
    expect(
      screen.queryByText('Completed with no output'),
    ).not.toBeInTheDocument();
  });

  it('keeps the error text when a session action fails', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'exec_session',
          args: '{"action":"read","process_id":"gone"}',
          result: 'exec_session: unknown process "gone"',
          status: 'error',
        })}
      />,
    );
    await user.click(
      screen.getByRole('button', { name: /session.*gone.*read/ }),
    );
    expect(screen.getByText(/unknown process/)).toBeInTheDocument();
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

  it('names the window a read_file result came from', async () => {
    const user = userEvent.setup();
    render(
      <ToolCard
        tool={tool({
          name: 'read_file',
          args: '{"file_path":"src/a.go","offset":40,"limit":2}',
          result: JSON.stringify({
            file_path: 'src/a.go',
            content: 'line 40\nline 41\n',
            offset: 40,
            limit: 2,
            total_lines: 200,
            is_truncated: true,
          }),
        })}
      />,
    );
    // "2 lines" alone would read as the whole file; the badge names the
    // range this call returned and flags what is still unread.
    const badge = screen.getByText('Lines 40–41 of 200');
    expect(badge).toHaveAttribute('data-tip', '159 more lines');
    await user.click(screen.getByRole('button', { name: /a\.go/i }));
    expect(screen.getByText(/line 40/)).toBeInTheDocument();
  });

  it('shows the frame the model saw when view_image expands', async () => {
    const user = userEvent.setup();
    const dataUrl = 'data:image/jpeg;base64,ZmFrZQ==';
    render(
      <ToolCard
        tool={tool({
          name: 'view_image',
          args: '{"path":"shots/hero.png"}',
          result: 'view_image: shots/hero.png (1440x900, 123456 bytes)',
          images: [{ data_url: dataUrl, media_type: 'image/jpeg' }],
        })}
      />,
    );
    const header = screen.getByRole('button', { name: /shots\/hero\.png/ });
    expect(header).toHaveTextContent('1440×900');
    // Collapsed until asked: the picture is the payload, not the header.
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    await user.click(header);
    // The inline part renders as-is — no round trip to disk, and the
    // caption names the frame as the (downscaled) one handed to the
    // model rather than the original on disk.
    expect(screen.getByRole('img')).toHaveAttribute('src', dataUrl);
    expect(screen.getByText('Model saw 1440×900 · 123.5k')).toBeInTheDocument();
    expect(apiMock.readPreview).not.toHaveBeenCalled();
  });

  it('renders the file from disk when the result carries no image part', async () => {
    const user = userEvent.setup();
    const dataUrl = 'data:image/png;base64,ZGlzaw==';
    apiMock.readPreview.mockResolvedValue({
      path: 'shots/hero.png',
      rel: 'shots/hero.png',
      root: 'workspace',
      name: 'hero.png',
      size: 2048,
      media_type: 'image/png',
      kind: 'image',
      data_url: dataUrl,
    });
    render(
      <ToolCard
        tool={tool({
          name: 'view_image',
          args: '{"path":"shots/hero.png"}',
          result: 'view_image: shots/hero.png (1440x900, 123456 bytes)',
        })}
      />,
    );
    await user.click(screen.getByRole('button', { name: /shots\/hero\.png/ }));
    // An archive that dropped the bytes, or a call that only described
    // the file: the picture still exists on disk, so show that one — and
    // say it is the file rather than crediting the model with it.
    expect(await screen.findByRole('img')).toHaveAttribute('src', dataUrl);
    // The model's frame is no longer what is on screen, so its
    // dimensions go with it.
    expect(screen.queryByText('1440×900')).not.toBeInTheDocument();
    expect(
      screen.getByText('Rendered from hero.png · 2.0k'),
    ).toBeInTheDocument();
  });

  it('ticks a running call and invents no duration for a replayed one', async () => {
    vi.useFakeTimers();
    const args = '{"command":"go build ./..."}';
    const { rerender } = render(
      <ToolCard
        tool={tool({
          name: 'exec_command',
          args,
          status: 'running',
          seenAt: Date.now(),
        })}
      />,
    );
    expect(screen.queryByText('<1s')).not.toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(screen.getAllByText('1s').length).toBeGreaterThan(0);
    // A result this client never saw start (a replayed session) carries
    // no local timing, so the card shows none instead of a guess.
    rerender(
      <ToolCard
        tool={tool({
          name: 'exec_command',
          args,
          result: '{"exit_code":0,"stdout":"","stderr":""}',
        })}
      />,
    );
    expect(screen.queryByText('1s')).not.toBeInTheDocument();
    vi.useRealTimers();
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

  it('summarizes the patch instead of repeating the result envelope', async () => {
    const patch =
      '*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch\n';
    apiMock.renderPatch.mockResolvedValueOnce([
      {
        path: 'a.txt',
        action: 'update',
        added: 1,
        removed: 1,
        lines: [{ kind: 'add', old_num: 0, new_num: 3, text: 'new' }],
      },
    ]);
    render(
      <ApplyPatchView
        tool={tool({
          name: 'apply_patch',
          args: JSON.stringify({ patch }),
          result: JSON.stringify({
            files: [{ path: 'a.txt', action: 'update' }],
          }),
        })}
      />,
    );
    // The card header carries the summary; the envelope JSON and the
    // per-file result list would only repeat what the diff shows.
    expect(await screen.findByText('1 file changed')).toBeInTheDocument();
    expect(
      screen.queryByText('{"files":[{"path":"a.txt","action":"update"}]}'),
    ).not.toBeInTheDocument();
    expect(screen.getAllByText('a.txt')).toHaveLength(1);
  });

  it('keeps the error text when the call failed without a diff', async () => {
    render(
      <ApplyPatchView
        tool={tool({
          name: 'apply_patch',
          args: JSON.stringify({ input: '*** Begin Patch\n*** End Patch\n' }),
          result: 'apply_patch: patch context does not match a.txt',
          status: 'error',
        })}
      />,
    );
    expect(
      await screen.findByText(
        'apply_patch: patch context does not match a.txt',
      ),
    ).toBeInTheDocument();
    // Nothing rendered a diff, so the header falls back to the tool name.
    expect(screen.getByText('apply_patch')).toBeInTheDocument();
  });

  it('falls back to the result file list when the renderer fails', async () => {
    const patch =
      '*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch\n';
    apiMock.renderPatch.mockRejectedValueOnce(
      new Error('apply_patch: patch context does not match a.txt'),
    );
    render(
      <ApplyPatchView
        tool={tool({
          name: 'apply_patch',
          args: JSON.stringify({ patch }),
          result: JSON.stringify({
            files: [{ path: 'a.txt', action: 'update' }],
          }),
          status: 'error',
        })}
      />,
    );
    // The raw patch stays visible (it is all the user has), and the list
    // below names what the backend did change.
    expect(await screen.findByText(/Begin Patch/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'a.txt' })).toBeInTheDocument();
  });
});

// The ask card's header row is one line: an icon, the question, the
// answer, a chevron. The answer used to be unbounded — a multi-choice
// answer joined its option texts, and three long ones made a chip wider
// than the card, which squeezed the question to nothing and painted both
// past the edge. The chip is capped and shortened now, and what it had to
// shorten stays reachable: on hover, and in the expanded body.
describe('AskUserView', () => {
  const OPTIONS = [
    'Merge #197 (the flake it fixes is the one that turns main red)',
    'Give TestSessionSignalInterrupts a timeout self-diagnosis (dump child /proc/<pid>/status)',
    'Track the execd flake down: rebuild the Linux environment with bwrap + sudo',
    'Add fuzz failure notifications to the release workflow',
    'Leave all of it for now, I will look at #197 first',
  ];

  function ask(overrides: Partial<ToolView> = {}) {
    return tool({
      name: 'ask_user',
      args: JSON.stringify({
        question: 'What next? (#197 all green pending merge, the execd flake)',
        kind: 'select',
        multiple: true,
        options: OPTIONS,
      }),
      ...overrides,
    });
  }

  it('names a multi-choice answer by its count, not by the option texts', () => {
    render(
      <ToolCard
        tool={ask({
          result: JSON.stringify({
            cancelled: false,
            choice: '',
            choices: OPTIONS.slice(0, 3),
            other: '',
            text: '',
          }),
        })}
      />,
    );

    const chip = screen.getByText('✓ 3 choices');
    // The question keeps its place in the row; the chip is capped so it
    // cannot take the row from it (the geometry itself is asserted in
    // e2e/chat.spec.ts, where a real layout can be measured).
    expect(chip.className).toMatch(/max-w-\[/);
    expect(chip.className).toContain('truncate');
    expect(
      screen.getByText(/What next\? \(#197 all green pending merge/),
    ).toBeInTheDocument();
    // Nothing is lost: the chip names what the joined texts said.
    expect(chip).toHaveAttribute('data-tip', OPTIONS.slice(0, 3).join(', '));
  });

  it('keeps a long single answer, shortened on the chip and whole on hover', () => {
    const answer =
      'Give TestSessionSignalInterrupts a timeout self-diagnosis (dump the child /proc/<pid>/status SigBlk + ps process group, so the next red CI carries its own evidence)';
    render(
      <ToolCard
        tool={ask({
          result: JSON.stringify({
            cancelled: false,
            choice: answer,
            choices: [],
            other: '',
            text: '',
          }),
        })}
      />,
    );

    const chip = screen.getByText(`✓ ${answer}`);
    expect(chip).toHaveAttribute('data-tip', answer);
    expect(chip.className).toMatch(/max-w-\[/);
  });

  it('names a written answer over the ticked options', () => {
    render(
      <ToolCard
        tool={ask({
          result: JSON.stringify({
            cancelled: false,
            choice: '',
            choices: OPTIONS.slice(0, 2),
            other: 'take the first two, I will read the rest later',
            text: '',
          }),
        })}
      />,
    );
    expect(
      screen.getByText('✓ take the first two, I will read the rest later'),
    ).toBeInTheDocument();
  });

  it('spells the multi-choice answer out once the card is expanded', async () => {
    const user = userEvent.setup();
    const chosen = OPTIONS.slice(0, 3);
    render(
      <ToolCard
        tool={ask({
          result: JSON.stringify({
            cancelled: false,
            choice: '',
            choices: chosen,
            other: '',
            text: '',
          }),
        })}
      />,
    );
    await user.click(screen.getByText('✓ 3 choices'));
    // Every option is listed, the ticked ones marked as chosen, and the
    // answer line repeats them in full — the chip's count is a summary,
    // not the only place the answer exists.
    for (const option of OPTIONS) {
      expect(screen.getAllByText(option).length).toBeGreaterThan(0);
    }
    expect(
      screen.getByText(chosen.join(', '), { exact: false }),
    ).toBeInTheDocument();
  });
});
