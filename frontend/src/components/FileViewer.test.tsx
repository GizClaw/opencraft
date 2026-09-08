import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { stateRoot } from '../state/app';
import { FileViewer } from './FileViewer';

const apiMock = vi.hoisted(() => ({
  listDir: vi.fn(async () => []),
  searchFiles: vi.fn(async () => []),
  readPreview: vi.fn(async () => ({
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
