package bindings

import (
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
	Graph       string `json:"graph"`
	CreatedAt   string `json:"created_at,omitempty"`
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
	return AgentDetail{
		Name:        spec.Name,
		Description: spec.Description,
		Graph:       spec.Graph,
		CreatedAt:   spec.CreatedAt.UTC().String(),
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
