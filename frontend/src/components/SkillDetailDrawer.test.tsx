import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import type { FilePreview, SkillDTO } from '../lib/types';
import { SkillDetailDrawer } from './SkillDetailDrawer';

const apiMock = vi.hoisted(() => ({
  skillContent: vi.fn(),
  resolveTarget: vi.fn(),
  readPreview: vi.fn(),
  openExternal: vi.fn(),
  revealArtifact: vi.fn(),
  openPath: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const skill: SkillDTO = {
  name: 'plan',
  description: 'Break tasks into an executable plan.',
  scope: 'user',
  path: '/tmp/.agents/skills/plan/SKILL.md',
};

const reference = {
  path: '/tmp/.agents/skills/plan/references/deploy.md',
  rel: 'references/deploy.md',
  root: 'skill',
  name: 'deploy.md',
  is_dir: false,
  size: 15,
  media_type: 'text/markdown',
};

function referencePreview(): FilePreview {
  return { ...reference, kind: 'text', text: 'Deploy the app' };
}

describe('SkillDetailDrawer', () => {
  beforeEach(() => {
    apiMock.skillContent.mockReset();
    apiMock.resolveTarget.mockReset();
    apiMock.readPreview.mockReset();
    apiMock.openExternal.mockReset();
    useStore.setState({ viewers: {} });
  });

  it('renders the full SKILL.md body as markdown', async () => {
    apiMock.skillContent.mockResolvedValue(
      '# Plan instructions\n\n```ts\nconst plan = true;\n```',
    );
    render(<SkillDetailDrawer skill={skill} onClose={() => {}} />);

    expect(
      await screen.findByRole('heading', { name: 'Plan instructions' }),
    ).toBeInTheDocument();
    expect(document.body.textContent).toContain('const plan = true');
    expect(
      screen.getByText('/tmp/.agents/skills/plan/SKILL.md'),
    ).toBeInTheDocument();
  });

  it('shows read errors in the drawer', async () => {
    apiMock.skillContent.mockRejectedValue(new Error('missing skill'));
    render(<SkillDetailDrawer skill={skill} onClose={() => {}} />);

    expect(
      await screen.findByText(/Failed to read SKILL\.md|读取 SKILL\.md 失败/),
    ).toBeInTheDocument();
  });

  it('opens a referenced file in a dialog instead of the chat file panel', async () => {
    apiMock.skillContent.mockResolvedValue(
      '# Plan instructions\n\n[deploy.md](references/deploy.md)',
    );
    apiMock.resolveTarget.mockResolvedValue(reference);
    apiMock.readPreview.mockResolvedValue(referencePreview());
    render(<SkillDetailDrawer skill={skill} onClose={() => {}} />);

    await screen.findByRole('heading', { name: 'Plan instructions' });
    const link = screen.getByRole('link', { name: 'deploy.md' });
    expect(link).toHaveAttribute('href', 'references/deploy.md');

    fireEvent.click(link);

    // The reference is resolved against the skill's own directory and
    // rendered inside the dialog, on top of the drawer.
    const dialog = await screen.findByRole('dialog', { name: 'deploy.md' });
    expect(
      await within(dialog).findByText('Deploy the app'),
    ).toBeInTheDocument();
    expect(apiMock.resolveTarget).toHaveBeenCalledWith(
      'references/deploy.md',
      '/tmp/.agents/skills/plan',
    );
    // The session-scoped file panel of the chat stays untouched: the
    // skills page owns no viewer tab.
    expect(useStore.getState().viewers).toEqual({});
  });

  it('routes external references to the system browser', async () => {
    apiMock.skillContent.mockResolvedValue(
      '# Plan instructions\n\n[docs](https://example.com/guide)',
    );
    render(<SkillDetailDrawer skill={skill} onClose={() => {}} />);

    await screen.findByRole('heading', { name: 'Plan instructions' });
    fireEvent.click(screen.getByRole('link', { name: 'docs' }));

    await waitFor(() =>
      expect(apiMock.openExternal).toHaveBeenCalledWith(
        'https://example.com/guide',
      ),
    );
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'docs' })).toBeNull();
  });
});
