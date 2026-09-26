import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mountOutsideAct, pressEscape } from '../../test/outsideAct';
import i18n from '../../i18n';
import { useStore } from '../../lib/store';
import type { FilePreview, ResolvedTarget } from '../../lib/types';
import { FilePreviewModal } from './FilePreviewModal';

const apiMock = vi.hoisted(() => ({
  readPreview: vi.fn(),
  resolveTarget: vi.fn(),
  openExternal: vi.fn(),
  revealArtifact: vi.fn(),
  openPath: vi.fn(),
}));

vi.mock('../../lib/api', () => ({ api: apiMock }));

function target(over: Partial<ResolvedTarget> = {}): ResolvedTarget {
  return {
    path: '/tmp/.agents/skills/plan/SKILL.md',
    rel: 'SKILL.md',
    root: 'skill',
    name: 'SKILL.md',
    is_dir: false,
    size: 12,
    media_type: 'text/markdown',
    ...over,
  };
}

function preview(text: string, over: Partial<FilePreview> = {}): FilePreview {
  return {
    path: '/tmp/.agents/skills/plan/SKILL.md',
    rel: 'SKILL.md',
    root: 'skill',
    name: 'SKILL.md',
    size: 12,
    media_type: 'text/markdown',
    kind: 'text',
    text,
    ...over,
  };
}

describe('FilePreviewModal', () => {
  beforeEach(() => {
    apiMock.readPreview.mockReset();
    apiMock.resolveTarget.mockReset();
    apiMock.openExternal.mockReset();
    useStore.setState({ toasts: [] });
  });

  it('renders the target with its path and closes on Escape', async () => {
    apiMock.readPreview.mockResolvedValue(preview('# Steps\n\nrun it once'));
    const onClose = vi.fn();
    render(<FilePreviewModal initial={target()} onClose={onClose} />);

    expect(
      await screen.findByRole('heading', { name: 'Steps' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'SKILL.md' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: i18n.t('files.linkBack') }),
    ).toBeNull();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });

  it('handles an Escape that arrives before passive effects flush', async () => {
    apiMock.readPreview.mockResolvedValue(preview('# Steps'));
    const onClose = vi.fn();
    const mount = mountOutsideAct(
      <FilePreviewModal initial={target()} onClose={onClose} />,
    );
    try {
      await mount.waitForDom(
        () => mount.container.querySelector('[role="dialog"]') !== null,
      );
      pressEscape();
      // The dialog is on screen, so it must own Escape already.
      expect(onClose).toHaveBeenCalledTimes(1);
    } finally {
      await mount.unmount();
    }
  });

  it('pushes a nested reference and pops it with the back arrow', async () => {
    apiMock.readPreview.mockImplementation(async (path: string) =>
      path.endsWith('deploy.md')
        ? preview('Deploy steps', {
            path,
            rel: 'references/deploy.md',
            name: 'deploy.md',
          })
        : preview('[deploy](references/deploy.md)'),
    );
    apiMock.resolveTarget.mockResolvedValue(
      target({
        path: '/tmp/.agents/skills/plan/references/deploy.md',
        rel: 'references/deploy.md',
        name: 'deploy.md',
      }),
    );
    render(<FilePreviewModal initial={target()} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'deploy' }));

    const dialog = await screen.findByRole('dialog', { name: 'deploy.md' });
    expect(await within(dialog).findByText('Deploy steps')).toBeInTheDocument();
    expect(
      within(dialog).getByText('references/deploy.md'),
    ).toBeInTheDocument();
    expect(apiMock.resolveTarget).toHaveBeenCalledWith(
      'references/deploy.md',
      '/tmp/.agents/skills/plan',
    );

    fireEvent.click(
      within(dialog).getByRole('button', { name: i18n.t('files.linkBack') }),
    );

    await waitFor(() =>
      expect(
        screen.getByRole('dialog', { name: 'SKILL.md' }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole('link', { name: 'deploy' })).toBeInTheDocument();
  });

  it('sends external references to the browser without replacing the page', async () => {
    apiMock.readPreview.mockResolvedValue(
      preview('[web](https://example.com/guide)'),
    );
    render(<FilePreviewModal initial={target()} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'web' }));

    await waitFor(() =>
      expect(apiMock.openExternal).toHaveBeenCalledWith(
        'https://example.com/guide',
      ),
    );
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
    expect(
      screen.getByRole('dialog', { name: 'SKILL.md' }),
    ).toBeInTheDocument();
  });

  it('flashes the reason a reference cannot be opened', async () => {
    apiMock.readPreview.mockResolvedValue(
      preview('[secret](../../secret.txt)'),
    );
    apiMock.resolveTarget.mockRejectedValue(
      new Error('file: resolve "../../secret.txt": no such file'),
    );
    render(<FilePreviewModal initial={target()} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'secret' }));

    await waitFor(() =>
      expect(useStore.getState().toasts.at(-1)?.text).toBe(
        'Error: file: resolve "../../secret.txt": no such file',
      ),
    );
    expect(
      screen.getByRole('dialog', { name: 'SKILL.md' }),
    ).toBeInTheDocument();
  });
});
