import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SkillDTO } from '../lib/types';
import { SkillDetailDrawer } from './SkillDetailDrawer';

const apiMock = vi.hoisted(() => ({
  skillContent: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const skill: SkillDTO = {
  name: 'plan',
  description: 'Break tasks into an executable plan.',
  scope: 'user',
  path: '/tmp/.agents/skills/plan/SKILL.md',
};

describe('SkillDetailDrawer', () => {
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

  it('keeps SKILL.md references clickable without navigation', async () => {
    apiMock.skillContent.mockResolvedValue(
      '# Plan instructions\n\n[deploy.md](references/deploy.md)',
    );
    render(<SkillDetailDrawer skill={skill} onClose={() => {}} />);

    await screen.findByRole('heading', { name: 'Plan instructions' });
    const link = screen.getByRole('link', { name: 'deploy.md' });
    expect(link).toHaveAttribute('href', 'references/deploy.md');

    fireEvent.click(link);

    expect(screen.getByRole('link', { name: 'deploy.md' })).toBeInTheDocument();
  });
});
