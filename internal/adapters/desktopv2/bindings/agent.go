package bindings

import (
	"encoding/json"
	"fmt"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

// Agent exposes persistent subagent lifecycle methods.
type Agent struct {
	core *core.Core
}

// NewAgentBinding wires the agent binding.
func NewAgentBinding(c *core.Core) *Agent {
	return &Agent{core: c}
}

// AgentSummary is the list view of one persistent subagent.
type AgentSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// GraphNode is the UI snapshot of one subagent graph node. Config is
// kept as raw JSON so the editor round-trips node-type-specific knobs.
type GraphNode struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

// GraphEdge is one directed transition in a subagent graph.
type GraphEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// Graph is the parsed flowcraft graph definition of one subagent.
type Graph struct {
	Name  string      `json:"name"`
	Entry string      `json:"entry"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// List returns every persisted subagent.
func (b *Agent) List() ([]AgentSummary, error) {
	h := b.core.Runtime.Current()
	if h == nil || h.Agents() == nil {
		return []AgentSummary{}, nil
	}
	summaries := h.Agents().List()
	out := make([]AgentSummary, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, AgentSummary{
			Name:        s.Name,
			Description: s.Description,
			CreatedAt:   s.CreatedAt,
		})
	}
	return out, nil
}

// AgentDetail is one agent declaration including its graph source.
type AgentDetail struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Graph       Graph  `json:"graph"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func parseAgentGraph(raw string) (Graph, error) {
	var g Graph
	if err := yaml.Unmarshal([]byte(raw), &g); err != nil {
		return Graph{}, fmt.Errorf("parse subagent graph: %w", err)
	}
	return g, nil
}

// Detail returns one agent declaration.
func (b *Agent) Detail(name string) (AgentDetail, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Agents() == nil {
		return AgentDetail{}, errNotReady("agent")
	}
	spec, err := h.Agents().Detail(ctx, name)
	if err != nil {
		return AgentDetail{}, err
	}
	graph, err := parseAgentGraph(spec.Graph)
	if err != nil {
		return AgentDetail{}, err
	}
	return AgentDetail{
		Name:        spec.Name,
		Description: spec.Description,
		Graph:       graph,
		CreatedAt:   spec.CreatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// Update changes one agent's description and/or graph.
func (b *Agent) Update(
	name, description, graph string,
) error {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Agents() == nil {
		return errNotReady("agent")
	}
	_, err := h.Agents().Update(ctx, name, description, graph)
	return err
}

// Unregister removes one persistent subagent.
func (b *Agent) Unregister(name string) error {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Agents() == nil {
		return errNotReady("agent")
	}
	return h.Agents().Remove(ctx, name)
}
