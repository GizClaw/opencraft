import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SkillsSection } from './ToolsPanel';
import type {
  SkillArchiveRow,
  SkillLifecycleState,
  SkillUsageRow,
} from '../lib/types';

const apiMock = vi.hoisted(() => ({
  skills: vi.fn(),
  skillLifecycle: vi.fn(),
  pinSkill: vi.fn(),
  unpinSkill: vi.fn(),
  retireSkill: vi.fn(),
  restoreSkill: vi.fn(),
  deleteSkill: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const skills = [
  {
    name: 'plan',
    description: 'Plan first',
    scope: 'user',
    path: '/u/skills/plan/SKILL.md',
  },
  {
    name: 'review',
    description: 'Built in',
    scope: 'builtin',
    path: '/app/skills/review/SKILL.md',
  },
];

const usageRows: SkillUsageRow[] = [
  {
    name: 'plan',
    scope: 'user',
    path: '/u/skills/plan/SKILL.md',
    uses: 4,
    last_used: '2026-01-02T03:04:05Z',
    pinned: true,
    retired: false,
    builtin: false,
    suggested_retire: false,
  },
  {
    name: 'review',
    scope: 'builtin',
    uses: 0,
    pinned: false,
    retired: false,
    builtin: true,
    suggested_retire: false,
  },
];

const archives: SkillArchiveRow[] = [
  {
    id: 'plan-20260101T000000',
    name: 'legacy',
    scope: 'user',
    skill_path: '/u/skills/legacy',
    archive_path: '/u/archive/legacy.tar.gz',
    created_at: '2026-01-01T00:00:00Z',
    restored: false,
  },
];

const lifecycle: SkillLifecycleState = {
  enabled: true,
  stale_after_days: 45,
  min_uses: 3,
  usage_window_days: 90,
  min_stale_after_days: 7,
  max_stale_after_days: 365,
  default_stale_after_days: 45,
  min_min_uses: 1,
  max_min_uses: 50,
  default_min_uses: 3,
  min_usage_window_days: 14,
  max_usage_window_days: 365,
  default_usage_window_days: 90,
  skills: usageRows,
  archives,
  usage_available: true,
  curator_available: true,
};

describe('SkillsSection lifecycle', () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMock)) fn.mockReset();
    apiMock.skills.mockResolvedValue(skills);
    apiMock.skillLifecycle.mockResolvedValue(lifecycle);
    apiMock.pinSkill.mockResolvedValue(undefined);
    apiMock.unpinSkill.mockResolvedValue(undefined);
    apiMock.retireSkill.mockResolvedValue(archives[0]);
    apiMock.restoreSkill.mockResolvedValue(archives[0]);
  });

  it('shows usage, pins and the archive list', async () => {
    render(<SkillsSection />);
    expect(await screen.findByText('plan')).toBeInTheDocument();
    expect(screen.getByText('4 uses')).toBeInTheDocument();
    expect(screen.getByText('Never used')).toBeInTheDocument();
    expect(screen.getByText('Pinned')).toBeInTheDocument();
    expect(screen.getByText('Archived skills')).toBeInTheDocument();
    expect(screen.getByText('legacy')).toBeInTheDocument();
  });

  it('pins and unpins a skill through the row menu', async () => {
    const user = userEvent.setup();
    render(<SkillsSection />);
    await screen.findByText('plan');
    // The builtin row has no menu, so the one that is there is plan's.
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Unpin' }));
    await waitFor(() => expect(apiMock.unpinSkill).toHaveBeenCalled());
    expect(apiMock.unpinSkill).toHaveBeenCalledWith('plan', 'user');
  });

  it('archives a skill only after the confirm', async () => {
    const user = userEvent.setup();
    render(<SkillsSection />);
    await screen.findByText('plan');
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Archive' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(apiMock.retireSkill).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Archive' }));
    await waitFor(() =>
      expect(apiMock.retireSkill).toHaveBeenCalledWith('plan', 'user'),
    );
    // The list is re-read so the row reflects what the curator did.
    expect(apiMock.skills.mock.calls.length).toBeGreaterThan(1);
    expect(apiMock.skillLifecycle.mock.calls.length).toBeGreaterThan(1);
  });

  it('restores an archived skill', async () => {
    const user = userEvent.setup();
    render(<SkillsSection />);
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    await waitFor(() =>
      expect(apiMock.restoreSkill).toHaveBeenCalledWith('plan-20260101T000000'),
    );
  });

  it('says so when there is no user database', async () => {
    apiMock.skillLifecycle.mockResolvedValue({
      ...lifecycle,
      skills: [],
      archives: [],
      usage_available: false,
      curator_available: false,
    });
    render(<SkillsSection />);
    expect(
      await screen.findByText(
        'No user database is open, so usage is not recorded and skills cannot be archived.',
      ),
    ).toBeInTheDocument();
    // Pin/archive are not offered when they cannot work.
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'More actions' }));
    expect(screen.queryByRole('menuitem', { name: 'Pin' })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: 'Archive' })).toBeNull();
  });
});
