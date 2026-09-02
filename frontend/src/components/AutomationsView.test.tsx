import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { AutomationsView } from './AutomationsView';

const apiMock = vi.hoisted(() => ({
  saveAutomation: vi.fn(),
  runAutomationNow: vi.fn(),
  deleteAutomation: vi.fn(),
  automationSessions: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
  useStore.setState({
    workspace: '/tmp/w',
    automations: [
      {
        id: 't-1',
        name: 'Daily brief',
        prompt: 'summarize',
        schedule: { type: 'daily', time: '09:00' },
        workspace: '/tmp/w',
        mode: 'workspace',
        model: '',
        think: 'medium',
        conversation_id: '',
        notify: 'always',
        enabled: true,
        created_at: '',
        updated_at: '',
        last_run_at: '',
        last_status: '',
        next_run_at: '',
      },
    ],
    automationRuns: {},
    modelOptions: [],
  });
});

describe('AutomationsView', () => {
  it('lists configured tasks', () => {
    render(<AutomationsView />);
    expect(screen.getByText('Daily brief')).toBeInTheDocument();
  });
});
