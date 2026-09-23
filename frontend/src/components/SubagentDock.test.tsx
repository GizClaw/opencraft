import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { SubagentDock } from './SubagentDock';

const apiMock = vi.hoisted(() => ({
  listAgents: vi.fn(),
  petActivities: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

// The dock listens to the one UI event channel, so the runtime's `On`
// has to hand back the handler for the test to emit through.
const events = vi.hoisted(() => ({
  handlers: new Map<string, (event: unknown) => void>(),
}));

vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (name: string, handler: (event: unknown) => void) => {
      events.handlers.set(name, handler);
      return () => events.handlers.delete(name);
    },
  },
}));

function emitStream(data: Record<string, unknown>) {
  const handler = events.handlers.get('opencraft:ui');
  if (!handler) throw new Error('the dock did not subscribe to opencraft:ui');
  handler({ data: { type: 'stream', data } });
}

const agents = [
  { name: 'planner', description: '', created_at: '' },
  { name: 'worker', description: '', created_at: '' },
];

const activities = [
  {
    agent_id: 'planner',
    conversation_id: 'conv-parent',
    run_id: 'run-parent',
    phase: 'tool' as const,
    tool: { name: 'list_dir', category: 'files' },
    severity: 0,
    ts: '2026-01-01T00:00:00Z',
  },
  {
    agent_id: 'worker',
    conversation_id: 'conv-child',
    run_id: 'run-child',
    phase: 'thinking' as const,
    severity: 0,
    ts: '2026-01-01T00:00:01Z',
  },
];

describe('SubagentDock', () => {
  beforeEach(() => {
    events.handlers.clear();
    apiMock.listAgents.mockReset();
    apiMock.petActivities.mockReset();
    apiMock.listAgents.mockResolvedValue(agents);
    apiMock.petActivities.mockResolvedValue(activities);
    useStore.setState({ resume: vi.fn() });
  });

  it('renders nothing while no subagent is active', async () => {
    apiMock.petActivities.mockResolvedValue([]);
    const { container } = render(<SubagentDock />);
    await waitFor(() => expect(apiMock.petActivities).toHaveBeenCalled());
    expect(container.firstChild).toBeNull();
  });

  it('nests a delegated run under the run that spawned it', async () => {
    const { container } = render(<SubagentDock />);
    await screen.findByText('planner');
    expect(screen.getByText('worker')).toBeInTheDocument();
    // Both are roots until a delta says otherwise.
    expect(container.querySelector('[data-parent="run-parent"]')).toBeNull();

    emitStream({ run_id: 'run-child', parent_run_id: 'run-parent' });
    await waitFor(() =>
      expect(
        container.querySelector('[data-parent="run-parent"]'),
      ).not.toBeNull(),
    );
    const child = container.querySelector('[data-depth="1"]');
    expect(child).not.toBeNull();
    expect(child?.textContent).toContain('worker');
  });

  it('collapses and expands the delegated runs', async () => {
    const user = userEvent.setup();
    const { container } = render(<SubagentDock />);
    await screen.findByText('planner');
    emitStream({ run_id: 'run-child', parent_run_id: 'run-parent' });
    await waitFor(() =>
      expect(container.querySelector('[data-depth="1"]')).not.toBeNull(),
    );

    await user.click(
      screen.getByRole('button', { name: 'Hide delegated runs' }),
    );
    expect(container.querySelector('[data-depth="1"]')).toBeNull();
    await user.click(
      screen.getByRole('button', { name: 'Show delegated runs' }),
    );
    expect(container.querySelector('[data-depth="1"]')).not.toBeNull();
  });

  it('opens the conversation of a nested run', async () => {
    const resume = vi.fn();
    useStore.setState({ resume });
    const user = userEvent.setup();
    render(<SubagentDock />);
    await screen.findByText('planner');
    emitStream({ run_id: 'run-child', parent_run_id: 'run-parent' });
    await screen.findByText('worker');
    await user.click(screen.getByText('worker'));
    expect(resume).toHaveBeenCalledWith('conv-child');
  });
});
