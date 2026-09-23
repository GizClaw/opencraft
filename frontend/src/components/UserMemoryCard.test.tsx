import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserMemoryCard } from './UserMemoryCard';
import type {
  MemoryFact,
  ReviewState,
  ReviewSuggestion,
  UserMemoryState,
} from '../lib/types';

const apiMock = vi.hoisted(() => ({
  userMemoryState: vi.fn(),
  memoryFacts: vi.fn(),
  reviewSettings: vi.fn(),
  reviewSuggestions: vi.fn(),
  addMemoryFact: vi.fn(),
  updateMemoryFact: vi.fn(),
  removeMemoryFact: vi.fn(),
  setMemoryFactStale: vi.fn(),
  saveUserMemorySettings: vi.fn(),
  acceptReviewSuggestion: vi.fn(),
  discardReviewSuggestion: vi.fn(),
  saveReviewSettings: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const state: UserMemoryState = {
  enabled: true,
  inject_max_items: 12,
  inject_max_chars: 2048,
  min_inject_max_items: 1,
  max_inject_max_items: 50,
  default_inject_max_items: 12,
  min_inject_max_chars: 256,
  max_inject_max_chars: 16384,
  default_inject_max_chars: 2048,
  max_items: 200,
  max_text_bytes: 4096,
  available: true,
  workspace: '/w/repo',
  live: 1,
  stale: 1,
};

const review: ReviewState = {
  enabled: false,
  every_turns: 5,
  min_tool_calls: 4,
  on_failure: true,
  max_suggestions: 3,
  timeout_seconds: 90,
  min_every_turns: 1,
  max_every_turns: 100,
  default_every_turns: 5,
  min_min_tool_calls: 0,
  max_min_tool_calls: 100,
  default_min_tool_calls: 4,
  min_max_suggestions: 1,
  max_max_suggestions: 10,
  min_timeout_seconds: 15,
  max_timeout_seconds: 600,
  available: true,
  pending: 1,
  accepted: 2,
  discarded: 0,
};

const liveFact: MemoryFact = {
  id: 'f-1',
  kind: 'environment',
  scope: 'global',
  text: 'The user runs macOS',
  source_conversation: 'conv-7',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-02T03:04:05Z',
  stale: false,
};

const staleFact: MemoryFact = {
  id: 'f-2',
  kind: 'convention',
  scope: 'workspace',
  workspace: '/w/repo',
  text: 'The repo uses tabs',
  stale: true,
};

const suggestion: ReviewSuggestion = {
  id: 'rs-1',
  status: 'pending',
  kind: 'memory',
  reason: 'stays true',
  source_conversation: 'conv-7',
  source_run: 'run-3',
  text: 'Prefers short answers',
  scope: 'global',
  candidate_kind: 'preference',
  payload: '{"text":"Prefers short answers"}',
};

describe('UserMemoryCard', () => {
  beforeEach(() => {
    for (const fn of Object.values(apiMock)) fn.mockReset();
    apiMock.userMemoryState.mockResolvedValue(state);
    apiMock.memoryFacts.mockResolvedValue([liveFact, staleFact]);
    apiMock.reviewSettings.mockResolvedValue(review);
    apiMock.reviewSuggestions.mockResolvedValue([suggestion]);
    apiMock.addMemoryFact.mockResolvedValue(liveFact);
    apiMock.updateMemoryFact.mockResolvedValue(liveFact);
    apiMock.removeMemoryFact.mockResolvedValue(undefined);
    apiMock.setMemoryFactStale.mockResolvedValue({ ...liveFact, stale: true });
    apiMock.saveUserMemorySettings.mockResolvedValue(undefined);
    apiMock.acceptReviewSuggestion.mockResolvedValue({
      suggestion: { ...suggestion, status: 'accepted' },
      fact: liveFact,
    });
    apiMock.discardReviewSuggestion.mockResolvedValue({
      ...suggestion,
      status: 'discarded',
    });
    apiMock.saveReviewSettings.mockResolvedValue(undefined);
  });

  it('lists the stored facts with their count and provenance', async () => {
    render(<UserMemoryCard />);
    expect(await screen.findByText('The user runs macOS')).toBeInTheDocument();
    expect(screen.getByText('The repo uses tabs')).toBeInTheDocument();
    // Scope badges separate a fact about the user from one about the
    // project, and a stopped fact stays visible marked rather than
    // disappearing.
    // 'Global'/'Workspace' also name the scope options of the add row,
    // so the assertion is on the badges being present at all.
    expect(screen.getAllByText('Global').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Workspace').length).toBeGreaterThan(0);
    expect(screen.getByText('Stale')).toBeInTheDocument();
    expect(screen.getByText('1 / 200 facts')).toBeInTheDocument();
    expect(screen.getByText('1 stale')).toBeInTheDocument();
    // One per fact with a source, plus the one behind the suggestion.
    expect(
      screen.getAllByRole('button', { name: 'View conversation' }),
    ).toHaveLength(2);
  });

  it('jumps to the conversation a fact came from', async () => {
    const onOpen = vi.fn();
    const user = userEvent.setup();
    render(<UserMemoryCard onOpenConversation={onOpen} />);
    await screen.findByText('The user runs macOS');
    await user.click(
      screen.getAllByRole('button', { name: 'View conversation' })[0],
    );
    // The workspace the turn ran in rides along so the caller can switch
    // to it before resuming.
    expect(onOpen.mock.calls[0][0]).toBe('conv-7');
  });

  it('remembers a new fact with the chosen scope', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('The user runs macOS');
    await user.type(
      screen.getByPlaceholderText('One sentence that stands on its own'),
      'Prefers exit codes over tracebacks',
    );
    await user.selectOptions(screen.getByLabelText('Scope'), 'workspace');
    await user.click(screen.getByRole('button', { name: /Remember/i }));
    await waitFor(() => expect(apiMock.addMemoryFact).toHaveBeenCalledTimes(1));
    expect(apiMock.addMemoryFact).toHaveBeenCalledWith({
      text: 'Prefers exit codes over tracebacks',
      scope: 'workspace',
    });
    // The list is re-read from the store rather than patched locally.
    expect(apiMock.memoryFacts.mock.calls.length).toBeGreaterThan(1);
  });

  it('stops injecting a fact and deletes one after a confirm', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('The user runs macOS');

    await user.click(screen.getByRole('button', { name: 'Stop injecting' }));
    await waitFor(() =>
      expect(apiMock.setMemoryFactStale).toHaveBeenCalledWith('f-1', true),
    );

    await user.click(screen.getAllByRole('button', { name: 'Delete fact' })[0]);
    const dialog = await screen.findByRole('alertdialog');
    await user.click(
      within(dialog).getByRole('button', { name: 'Delete fact' }),
    );
    await waitFor(() =>
      expect(apiMock.removeMemoryFact).toHaveBeenCalledWith('f-1'),
    );
  });

  it('edits a fact in place', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('The user runs macOS');
    await user.click(screen.getAllByRole('button', { name: 'Edit fact' })[0]);
    const field = screen.getByRole('textbox', { name: 'Edit fact' });
    await user.clear(field);
    await user.type(field, 'The user runs macOS 15');
    await user.click(screen.getByRole('button', { name: 'Save fact' }));
    await waitFor(() =>
      expect(apiMock.updateMemoryFact).toHaveBeenCalledWith(
        'f-1',
        'The user runs macOS 15',
      ),
    );
  });

  it('saves the injection budget', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('The user runs macOS');
    await user.click(screen.getByLabelText('Inject facts each turn'));
    const items = screen.getByLabelText(/Facts per turn/);
    await user.clear(items);
    await user.type(items, '8');
    const chars = screen.getByLabelText(/Injected bytes/);
    await user.clear(chars);
    await user.type(chars, '3072');
    await user.click(screen.getAllByRole('button', { name: /Save/i })[0]);
    await waitFor(() =>
      expect(apiMock.saveUserMemorySettings).toHaveBeenCalledWith({
        enabled: false,
        inject_max_items: 8,
        inject_max_chars: 3072,
      }),
    );
    // The state is re-read, so the card never shows a value the store
    // did not accept.
    expect(apiMock.userMemoryState.mock.calls.length).toBeGreaterThan(1);
  });

  it('accepts a queued suggestion through the memory write path', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('Prefers short answers');
    expect(screen.getByText('Why: stays true')).toBeInTheDocument();
    expect(screen.getByText('run-3')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Accept' }));
    await waitFor(() =>
      expect(apiMock.acceptReviewSuggestion).toHaveBeenCalledWith('rs-1'),
    );
    // Accepting writes a fact, so both halves of the card refresh.
    expect(apiMock.memoryFacts.mock.calls.length).toBeGreaterThan(1);
    expect(apiMock.reviewSuggestions.mock.calls.length).toBeGreaterThan(1);
  });

  it('discards a suggestion without touching memory', async () => {
    const user = userEvent.setup();
    render(<UserMemoryCard />);
    await screen.findByText('Prefers short answers');
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    await waitFor(() =>
      expect(apiMock.discardReviewSuggestion).toHaveBeenCalledWith('rs-1'),
    );
    expect(apiMock.acceptReviewSuggestion).not.toHaveBeenCalled();
    expect(apiMock.addMemoryFact).not.toHaveBeenCalled();
  });

  it('reports the read-only state without a user database', async () => {
    apiMock.userMemoryState.mockResolvedValue({
      ...state,
      available: false,
      live: 0,
      stale: 0,
    });
    apiMock.memoryFacts.mockResolvedValue([]);
    apiMock.reviewSettings.mockResolvedValue({
      ...review,
      available: false,
      pending: 0,
      accepted: 0,
    });
    apiMock.reviewSuggestions.mockResolvedValue([]);
    render(<UserMemoryCard />);
    expect(
      await screen.findByText(
        'No user database is open, so long-term memory is read-only here.',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('No user database is open, so the queue stays empty.'),
    ).toBeInTheDocument();
    expect(screen.getByText('No long-term facts yet.')).toBeInTheDocument();
    expect(
      screen.getByText('Nothing waiting for a verdict.'),
    ).toBeInTheDocument();
  });
});
