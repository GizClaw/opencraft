// Regression: a subagent graph source may omit the nodes/edges keys
// entirely — a single-node graph has no transitions — and the Go
// binding marshals the parsed nil slices as null. Opening the editor on
// such an agent used to crash the whole UI inside the canvas layout
// (`for...of graph.edges` over null). This pins the path from the raw
// binding payload through the api adapter to the rendered canvas.
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentGraphEditor } from './GraphView';

const agentMock = vi.hoisted(() => ({ Detail: vi.fn() }));

vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/agent',
  () => agentMock,
);

beforeEach(() => {
  vi.clearAllMocks();
});

describe('AgentGraphEditor', () => {
  it('renders a graph whose source omits the edges key', async () => {
    agentMock.Detail.mockResolvedValue({
      name: 'worker',
      description: 'does work',
      graph: {
        name: 'worker',
        entry: 'llm',
        nodes: [{ id: 'llm', type: 'inference', config: { all_tools: true } }],
        edges: null,
      },
    });

    render(
      <AgentGraphEditor
        agentName="worker"
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );

    expect(await screen.findByText('llm')).toBeInTheDocument();
    expect(screen.getByText('inference')).toBeInTheDocument();
  });
});
