// shell/viewers.ts: what a file target opens in - viewer vs browser.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import { primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('viewer targets', () => {
  it('treats Windows drive paths as local viewer targets', async () => {
    apiMock.resolveTarget.mockResolvedValue({
      path: 'C:\\Users\\me\\report.md',
      rel: '',
      root: 'workspace',
      name: 'report.md',
      is_dir: false,
      size: 10,
      media_type: 'text/markdown',
    });

    await useStore.getState().openFileTarget('C:\\Users\\me\\report.md');

    expect(apiMock.resolveTarget).toHaveBeenCalledWith(
      'C:\\Users\\me\\report.md',
      '',
    );
    expect(apiMock.openExternal).not.toHaveBeenCalled();
    const viewer = useStore.getState().viewers['s-1'];
    expect(viewer?.fileActive).toBe('C:\\Users\\me\\report.md');
  });

  it('routes url-like targets to the system browser instead of the viewer', async () => {
    await useStore.getState().openFileTarget('https://example.com');

    expect(apiMock.openExternal).toHaveBeenCalledWith('https://example.com');
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
  });

  it('replaces only the active placeholder tab when a file opens', () => {
    useStore.setState({
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [
            {
              key: 'blank-1',
              path: '',
              rel: '',
              root: 'workspace',
              name: 'New file',
              media_type: '',
            },
            {
              key: 'blank-2',
              path: '',
              rel: '',
              root: 'workspace',
              name: 'New file',
              media_type: '',
            },
          ],
          fileActive: 'blank-1',
          fileTreeDir: '.',
        },
      },
    });

    useStore.getState().openResolvedTarget({
      path: '/tmp/w/a.go',
      rel: 'a.go',
      root: 'workspace',
      name: 'a.go',
      is_dir: false,
      size: 10,
      media_type: 'text/plain',
    });

    const viewer = useStore.getState().viewers['s-1'];
    expect(viewer?.fileTabs.map((t) => t.key)).toEqual([
      'blank-2',
      '/tmp/w/a.go',
    ]);
    expect(viewer?.fileActive).toBe('/tmp/w/a.go');
  });
});
