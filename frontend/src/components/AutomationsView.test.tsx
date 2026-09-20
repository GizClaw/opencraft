import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
  // Opening the editor asks for the workspace's sessions.
  apiMock.automationSessions.mockResolvedValue([]);
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
        timeout: '2h',
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

  it('edits the run limit in minutes and saves it as a duration', async () => {
    apiMock.saveAutomation.mockResolvedValue({ id: 't-1' });
    render(<AutomationsView />);
    fireEvent.click(screen.getByText('Daily brief'));
    const limit = screen.getByRole('spinbutton') as HTMLInputElement;
    // The stored task says 2h; the form edits minutes.
    expect(limit.value).toBe('120');
    fireEvent.change(limit, { target: { value: '30' } });
    fireEvent.click(screen.getByText('Save'));
    await waitFor(() =>
      expect(apiMock.saveAutomation).toHaveBeenCalledTimes(1),
    );
    const saved = apiMock.saveAutomation.mock.calls[0][0] as {
      timeout: string;
    };
    expect(saved.timeout).toBe('30m');
  });

  it('refuses an unusable run limit instead of saving it', () => {
    render(<AutomationsView />);
    fireEvent.click(screen.getByText('Daily brief'));
    fireEvent.change(screen.getByRole('spinbutton'), {
      target: { value: '0' },
    });
    fireEvent.click(screen.getByText('Save'));
    expect(
      screen.getByText(/Run limit must be a whole number/),
    ).toBeInTheDocument();
    expect(apiMock.saveAutomation).not.toHaveBeenCalled();
  });
});
