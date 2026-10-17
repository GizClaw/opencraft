// What the graph editor hands its canvas.
//
// React Flow places an edge from the handle bounds it measures through the
// DOM (offsetWidth plus ResizeObserver), and jsdom has no layout: no edge
// reaches the DOM in a unit test, so a query for the condition pill would
// pass whatever the editor did. The canvas is where the editor's output is
// observable here, so this suite records the props it is rendered with —
// the same path the condition pill, the dashes and the node positions come
// from in the browser.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentGraphEditor } from './GraphView';

type CanvasProps = {
  edges: Array<{
    id: string;
    source: string;
    target: string;
    data?: { condition?: string };
  }>;
  nodes: Array<{ id: string }>;
  onEdgeClick: (
    event: unknown,
    edge: { source: string; target: string },
  ) => void;
  onNodesChange: (changes: unknown[]) => void;
};

const canvas = vi.hoisted(() => ({ props: null as CanvasProps | null }));

vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>();
  return {
    ...actual,
    ReactFlow: (props: CanvasProps) => {
      canvas.props = props;
      return null;
    },
    useReactFlow: () => ({ fitView: () => undefined }),
  };
});

const agentMock = vi.hoisted(() => ({ Detail: vi.fn() }));

vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/agent',
  () => agentMock,
);

const noop = () => {};

/** canvasProps is the props the canvas was last rendered with. */
function canvasProps(): CanvasProps {
  if (canvas.props === null) throw new Error('the canvas has not rendered');
  return canvas.props;
}

beforeEach(() => {
  canvas.props = null;
  vi.clearAllMocks();
  agentMock.Detail.mockResolvedValue({
    name: 'worker',
    description: 'does work',
    graph: {
      name: 'worker',
      entry: 'llm',
      nodes: [
        { id: 'llm', type: 'inference', config: {} },
        { id: 'gate', type: 'script', config: {} },
      ],
      edges: [{ from: 'llm', to: 'gate' }],
    },
  });
});

describe('AgentGraphEditor canvas', () => {
  it('hands the canvas the condition the user just typed', async () => {
    // The edge's condition is drawn from its own edge data (dashed stroke,
    // condition pill). It used to be left out of the key the edges were
    // rebuilt from, so the inspector and the canvas disagreed until the
    // graph's shape changed — across a save and reload too.
    render(
      <AgentGraphEditor agentName="worker" onClose={noop} onSaved={noop} />,
    );
    await waitFor(() => expect(canvasProps().edges).toHaveLength(1));
    expect(canvasProps().edges[0].data?.condition).toBeUndefined();

    // Selecting an edge is a click on the canvas; that is how the canvas
    // reports it.
    act(() => {
      canvasProps().onEdgeClick({}, { source: 'llm', target: 'gate' });
    });
    const field = screen.getByPlaceholderText('tool_pending == true');
    fireEvent.change(field, { target: { value: 'pending' } });

    expect(canvasProps().edges[0].data?.condition).toBe('pending');
  });

  it('does not dirty the document for a node drag', async () => {
    render(
      <AgentGraphEditor agentName="worker" onClose={noop} onSaved={noop} />,
    );
    await waitFor(() => expect(canvasProps().nodes).toHaveLength(2));

    act(() => {
      canvasProps().onNodesChange([
        {
          type: 'position',
          id: 'llm',
          position: { x: 10, y: 20 },
          dragging: false,
        },
      ]);
    });

    // Nothing saveable moved: save() writes { id, type, config } and the
    // next load lays the nodes out again, so a drag cannot be honoured and
    // must not raise the unsaved-changes prompt the close button reads.
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument();

    // The marker is what that assertion reads, so hold it down: an edit the
    // document does carry still raises it.
    fireEvent.change(screen.getByDisplayValue('does work'), {
      target: { value: 'does more work' },
    });
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  });
});
