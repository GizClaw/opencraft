import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { DEFAULT_UI_SETTINGS } from '../lib/appearance';
import type { FilePreview } from '../lib/types';
import { stateRoot } from '../state/app';
import { FileViewer } from './FileViewer';

const apiMock = vi.hoisted(() => ({
  listDir: vi.fn(
    async (): Promise<{ name: string; path: string; is_dir: boolean }[]> => [],
  ),
  searchFiles: vi.fn(async () => []),
  setUISettings: vi.fn(async () => undefined),
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

  it('lists hidden entries only after the switch is on', async () => {
    useStore.setState({ uiSettings: { ...DEFAULT_UI_SETTINGS } });
    apiMock.listDir.mockResolvedValue([]);
    render(<FileViewer sessionID="s-1" />);
    (await screen.findByTitle('Toggle file tree')).click();

    // Off by default: a directory holding only dot-entries reads empty.
    // The panel is open: its section header and the root row both read
    // "Workspace".
    expect(await screen.findAllByText('Workspace')).not.toHaveLength(0);
    expect(apiMock.listDir).toHaveBeenCalledWith('.', false);
    expect(screen.queryByText('.env')).not.toBeInTheDocument();

    // The switch asks the binding for hidden entries and persists the
    // preference so the next panel opens with the same answer.
    apiMock.listDir.mockResolvedValue([
      { name: '.github', path: '.github', is_dir: true },
      { name: '.env', path: '.env', is_dir: false },
    ]);
    (await screen.findByTitle('Show hidden files')).click();

    expect(await screen.findByText('.env')).toBeInTheDocument();
    expect(await screen.findByText('.github')).toBeInTheDocument();
    expect(apiMock.listDir).toHaveBeenCalledWith('.', true);
    expect(apiMock.setUISettings).toHaveBeenCalledWith(
      expect.objectContaining({ showHiddenFiles: true }),
    );

    useStore.setState({ uiSettings: { ...DEFAULT_UI_SETTINGS } });
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

  it('hides the tab strip scrollbar and keeps the active tab in view', async () => {
    const scrollIntoView = vi
      .spyOn(Element.prototype, 'scrollIntoView')
      .mockImplementation(() => {});
    try {
      const fileTab = (name: string) => ({
        key: `/tmp/w/internal/${name}`,
        path: `/tmp/w/internal/${name}`,
        rel: `internal/${name}`,
        root: 'workspace',
        name,
        media_type: 'text/plain',
      });
      const tabs = ['a.go', 'b.go', 'c.go'].map(fileTab);
      useStore.setState({
        viewers: {
          's-1': {
            filesOpen: true,
            panelMode: 'files',
            fileTabs: tabs,
            fileActive: tabs[0].key,
            fileTreeDir: '.',
          },
        },
      });
      const { container } = render(<FileViewer sessionID="s-1" />);

      const strip = container.querySelector('.no-scrollbar');
      expect(strip).not.toBeNull();
      // The new-tab button must not scroll away with the tabs.
      expect(
        strip?.contains(screen.getByRole('button', { name: 'New blank tab' })),
      ).toBe(false);
      await waitFor(() =>
        expect(scrollIntoView).toHaveBeenCalledWith({
          inline: 'nearest',
          block: 'nearest',
        }),
      );

      scrollIntoView.mockClear();
      useStore.setState((state) => ({
        viewers: {
          ...state.viewers,
          's-1': { ...state.viewers['s-1'], fileActive: tabs[2].key },
        },
      }));
      await waitFor(() => expect(scrollIntoView).toHaveBeenCalledTimes(1));
    } finally {
      scrollIntoView.mockRestore();
    }
  });

  it('maps a vertical wheel over the strip to horizontal tab scrolling', () => {
    render(<FileViewer sessionID="s-1" />);
    const strip = document.querySelector<HTMLDivElement>('.no-scrollbar');
    expect(strip).not.toBeNull();
    if (!strip) return;
    // jsdom has no layout: give the strip an overflowing box by hand.
    let scrollLeft = 0;
    Object.defineProperty(strip, 'scrollLeft', {
      configurable: true,
      get: () => scrollLeft,
      set: (value: number) => {
        scrollLeft = value;
      },
    });
    Object.defineProperty(strip, 'scrollWidth', { value: 600 });
    Object.defineProperty(strip, 'clientWidth', { value: 300 });

    const event = new WheelEvent('wheel', {
      deltaY: 120,
      bubbles: true,
      cancelable: true,
    });
    strip.dispatchEvent(event);

    expect(strip.scrollLeft).toBe(120);
    expect(event.defaultPrevented).toBe(true);
  });

  it('fades only the strip edges that hide tabs', () => {
    render(<FileViewer sessionID="s-1" />);
    const strip = document.querySelector<HTMLDivElement>('.no-scrollbar');
    expect(strip).not.toBeNull();
    if (!strip) return;
    // jsdom has no layout: give the strip an overflowing box by hand.
    let scrollLeft = 0;
    Object.defineProperty(strip, 'scrollLeft', {
      configurable: true,
      get: () => scrollLeft,
      set: (value: number) => {
        scrollLeft = value;
      },
    });
    Object.defineProperty(strip, 'scrollWidth', { value: 600 });
    Object.defineProperty(strip, 'clientWidth', { value: 300 });
    const sync = () => act(() => void strip.dispatchEvent(new Event('scroll')));

    // At the start only the right edge hides tabs.
    sync();
    expect(strip.style.maskImage).toContain('calc(100% - 1.25rem)');
    expect(strip.style.maskImage).not.toContain('transparent 0');

    // Mid-scroll both edges hide tabs.
    scrollLeft = 150;
    sync();
    expect(strip.style.maskImage).toContain('transparent 0');
    expect(strip.style.maskImage).toContain('calc(100% - 1.25rem)');

    // At the end only the left edge hides tabs.
    scrollLeft = 300;
    sync();
    expect(strip.style.maskImage).toContain('transparent 0');
    expect(strip.style.maskImage).not.toContain('calc(100% - 1.25rem)');
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
