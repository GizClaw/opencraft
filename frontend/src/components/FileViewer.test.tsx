import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import type { FilePreview } from '../lib/types';
import { stateRoot } from '../state/app';
import { FileViewer } from './FileViewer';

const apiMock = vi.hoisted(() => ({
  listDir: vi.fn(async () => []),
  searchFiles: vi.fn(async () => []),
  readPreview: vi.fn(async (): Promise<FilePreview> => ({
    path: '/tmp/w/internal/a.go',
    rel: 'internal/a.go',
    root: 'workspace',
    name: 'a.go',
    size: 12,
    media_type: 'text/plain',
    kind: 'text',
    text: 'package files\n',
  })),
  resolveTarget: vi.fn(),
  openPath: vi.fn(),
  revealArtifact: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

describe('FileViewer', () => {
  beforeEach(() => {
    useStore.setState({
      workspace: '/tmp/w',
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [
            {
              key: '/tmp/w/internal/a.go',
              path: '/tmp/w/internal/a.go',
              rel: 'internal/a.go',
              root: 'workspace',
              name: 'a.go',
              media_type: 'text/plain',
            },
          ],
          fileActive: '/tmp/w/internal/a.go',
          fileTreeDir: '.',
        },
      },
    });
  });

  it('renders an open file tab without crashing', async () => {
    render(<FileViewer sessionID="s-1" />);
    await waitFor(() => expect(apiMock.readPreview).toHaveBeenCalled(), {
      timeout: 5000,
    });
    await waitFor(
      () => {
        expect(document.querySelector('.cm-content')?.textContent).toContain(
          'package files',
        );
      },
      { timeout: 5000 },
    );
    expect(screen.getAllByText('a.go').length).toBeGreaterThan(0);
  });

  it('plays a video preview from the loopback stream URL', async () => {
    apiMock.readPreview.mockResolvedValueOnce({
      path: '/tmp/w/generated/clip.mp4',
      rel: 'generated/clip.mp4',
      root: 'workspace',
      name: 'clip.mp4',
      size: 4096,
      media_type: 'video/mp4',
      kind: 'video',
      stream_url: 'http://127.0.0.1:1/media/token/generated/clip.mp4',
    });
    useStore.setState({
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [
            {
              key: '/tmp/w/generated/clip.mp4',
              path: '/tmp/w/generated/clip.mp4',
              rel: 'generated/clip.mp4',
              root: 'workspace',
              name: 'clip.mp4',
              media_type: 'video/mp4',
            },
          ],
          fileActive: '/tmp/w/generated/clip.mp4',
          fileTreeDir: '.',
        },
      },
    });
    render(<FileViewer sessionID="s-1" />);
    await waitFor(() => {
      expect(document.querySelector('video')?.getAttribute('src')).toBe(
        'http://127.0.0.1:1/media/token/generated/clip.mp4',
      );
    });
  });

  it('opens a blank tab and shows the file tree on plus', async () => {
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
    const actor = stateRoot.registry.ensure('s-1', {
      workspaceGeneration: stateRoot.generation(),
    });
    actor?.send({ type: 'NEW_CHAT_READY' });
    useStore.setState({
      workspace: '/tmp/w',
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [],
          fileActive: null,
          fileTreeDir: '.',
        },
      },
    });
    render(<FileViewer sessionID="s-1" />);

    await screen.findByRole('button', { name: 'New blank tab' });
    (await screen.findByRole('button', { name: 'New blank tab' })).click();

    expect(await screen.findByText('New file')).toBeInTheDocument();
    await waitFor(() => expect(apiMock.listDir).toHaveBeenCalled());
  });
});
