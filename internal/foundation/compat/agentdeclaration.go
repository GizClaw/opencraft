package compat

import (
	"time"

	"sigs.k8s.io/yaml"
)

// This file owns the pre-version subagent declaration: the flat
// name/description/graph record the 0.1.0 build wrote to
// ~/.opencraft/agents/<name>/agent.yaml before declarations carried a
// card, an engine reference and a format version. The current shape
// belongs to capabilities/agents, which also rewrites a file it read
// through here, so this package only recognises the old one and hands
// the fields over as a payload.

// AgentDeclaration is a subagent declaration as an older build wrote
// it. It carries only the fields that format had; the caller builds
// its own declaration type from them.
type AgentDeclaration struct {
	Name        string
	Description string
	// Graph is the complete flowcraft graph source (JSON or YAML
	// text), the field the flat format stored the whole agent in.
	Graph     string
	CreatedAt time.Time
}

// LegacyAgentDeclaration decodes one pre-version subagent
// declaration. ok=false means the document is not a legacy
// declaration and the caller must parse it as the current format.
//
// The shape is decided by the document, not by the version field: a
// declaration that carries card or version is current even when a
// top-level graph string is also present, because the versioned
// format stores its graph under engine.settings. The decode is strict
// for the same reason the current format's is — a declaration must
// not look like it controls host-owned wiring.
func LegacyAgentDeclaration(data []byte) (AgentDeclaration, bool, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return AgentDeclaration{}, false, err
	}
	if _, ok := probe["graph"].(string); !ok {
		return AgentDeclaration{}, false, nil
	}
	if _, ok := probe["card"]; ok {
		return AgentDeclaration{}, false, nil
	}
	if _, ok := probe["version"]; ok {
		return AgentDeclaration{}, false, nil
	}
	var legacy struct {
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Graph       string    `json:"graph"`
		CreatedAt   time.Time `json:"created_at,omitempty"`
	}
	if err := yaml.UnmarshalStrict(data, &legacy); err != nil {
		return AgentDeclaration{}, false, err
	}
	return AgentDeclaration{
		Name:        legacy.Name,
		Description: legacy.Description,
		Graph:       legacy.Graph,
		CreatedAt:   legacy.CreatedAt,
	}, true, nil
}
