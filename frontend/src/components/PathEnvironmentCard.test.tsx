import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PathEnvironmentCard } from './PathEnvironmentCard';
import type { PathEnvironment } from '../lib/types';

const apiMock = vi.hoisted(() => ({
  pathEnvironment: vi.fn(),
  setPathPrepend: vi.fn(),
  resolvePath: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('PathEnvironmentCard', () => {
  it('renders the resolved PATH with the source of every entry', async () => {
    apiMock.pathEnvironment.mockResolvedValue({
      path: '/opt/homebrew/bin:/usr/bin',
      segments: [
        { dir: '/opt/homebrew/bin', source: 'candidate', present: true },
        { dir: '/usr/bin', source: 'inherited', present: true },
        { dir: '/opt/stale', source: 'prepend', present: false },
      ],
      prepend: ['/opt/stale'],
      rejected: ['relative/bin'],
      missing: [],
      reloaded: true,
    } satisfies PathEnvironment);

    render(<PathEnvironmentCard />);

    expect(await screen.findByText('/usr/bin')).toBeInTheDocument();
    expect(screen.getByText('/opt/homebrew/bin')).toBeInTheDocument();
    expect(screen.getByText('added')).toBeInTheDocument();
    expect(screen.getByText('inherited')).toBeInTheDocument();
    // A prepend entry that is not on disk yet is kept and flagged instead
    // of being dropped silently.
    expect(screen.getAllByText(/directory not present/).length).toBeGreaterThan(
      0,
    );
    expect(screen.getByDisplayValue('/opt/stale')).toBeInTheDocument();
  });

  // The regression: the host used to marshal an empty Go slice as null, and
  // `.length` on it blanked the settings page.
  it('survives a host that sends null lists', async () => {
    apiMock.pathEnvironment.mockResolvedValue({
      path: '/usr/bin',
      segments: null,
      prepend: null,
      rejected: null,
      missing: null,
      reloaded: true,
    } as unknown as PathEnvironment);

    render(<PathEnvironmentCard />);

    expect(await screen.findByText('Process PATH')).toBeInTheDocument();
  });
});
