import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PetBehaviorPanel } from './PetBehaviorPanel';

const apiMock = vi.hoisted(() => ({
  petDiagnostics: vi.fn(),
  petRuntimeStatus: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

// The panel listens to the one UI event channel, so the runtime's `On`
// has to hand back the handler for the test to emit through — and the
// channel it registered on is the assertion that used to be wrong (the
// mount report is an event *type*, not a channel of its own).
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

function emitEnvelope(envelope: unknown) {
  const handler = events.handlers.get('opencraft:ui');
  if (!handler) throw new Error('the panel did not subscribe to opencraft:ui');
  handler({ data: envelope });
}

function emitRuntimeStatus(data: unknown) {
  emitEnvelope({ type: 'pet:runtime_status', data });
}

const report = {
  pack_id: 'assistant-default',
  artboard: 'Pet',
  view_model: 'PetVM',
  ok: false,
  missing: ['binding "walking": property "galloping"'],
};

const diagnostics = {
  drives: { attention: 60, energy: 40, comfort: 50 },
  mood: 'content',
  stats: { poke_count: 3 },
  disposition: 'idle',
  phase: 'idle',
  walking: false,
  hovered: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  events.handlers.clear();
  apiMock.petDiagnostics.mockResolvedValue(diagnostics);
  // The poll starts out knowing nothing: the pet window has not reported
  // yet, so anything the panel shows came from the pushed event.
  apiMock.petRuntimeStatus.mockResolvedValue({ status: null, reported: false });
});

describe('PetBehaviorPanel', () => {
  it('renders the mount report pushed on the UI channel, not only the poll', async () => {
    render(<PetBehaviorPanel />);
    // The first poll answers "reported: false", so the character row is
    // empty until the push arrives.
    await waitFor(() =>
      expect(apiMock.petRuntimeStatus).toHaveBeenCalledTimes(1),
    );
    expect(screen.getByText('Character').parentElement).toHaveTextContent('—');

    emitRuntimeStatus(report);

    await waitFor(() =>
      expect(screen.getByText('assistant-default')).toBeInTheDocument(),
    );
    expect(screen.getByText('Degraded')).toBeInTheDocument();
    expect(
      screen.getByText('binding "walking": property "galloping"'),
    ).toBeInTheDocument();
  });

  it('ignores other event types on the shared channel', async () => {
    render(<PetBehaviorPanel />);
    await waitFor(() =>
      expect(apiMock.petRuntimeStatus).toHaveBeenCalledTimes(1),
    );
    emitRuntimeStatus(report);

    emitEnvelope({ type: 'pet:settings_changed', data: { enabled: false } });

    await waitFor(() =>
      expect(screen.getByText('assistant-default')).toBeInTheDocument(),
    );
  });
});
