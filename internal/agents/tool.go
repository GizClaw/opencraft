package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/tools/confirm"
)

const (
	// CreateName is the canonical create_agent tool name.
	CreateName = "create_agent"
	// UpdateName is the canonical update_agent tool name.
	UpdateName = "update_agent"
	// RemoveName is the canonical unregister_agent tool name.
	RemoveName = "unregister_agent"
)

// Tool bundles the subagent lifecycle tools over one registry.
type Tool struct {
	lifecycle *Lifecycle
}

// NewTool creates the tools. lifecycle is required.
func NewTool(lifecycle *Lifecycle) (*Tool, error) {
	if lifecycle == nil {
		return nil, errdefs.Validationf("agents tool: lifecycle is required")
	}
	return &Tool{lifecycle: lifecycle}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(lifecycle *Lifecycle) *Tool {
	t, err := NewTool(lifecycle)
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the create_agent and unregister_agent tools.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{
		createTool{t.lifecycle},
		updateTool{t.lifecycle},
		removeTool{t.lifecycle},
	}
}

type createTool struct{ lifecycle *Lifecycle }

var _ tool.Tool = createTool{}

func (createTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		CreateName,
		"Creates a persistent subagent running on a caller-supplied "+
			"flowcraft graph definition, and makes it available as a "+
			"delegation target. graph is the complete GraphDefinition "+
			"(JSON or YAML): nodes, edges, and the inference node's "+
			"system_prompt define the agent's behavior. Author the "+
			"definition with the flowcraft-config skill: if it is not "+
			"already available, install it first with skill_install "+
			"flowcraft-config, then follow its graph authoring "+
			"reference before calling this tool. Workflow: create_agent "+
			"→ delegate to the new name → delegation_status → "+
			"unregister_agent when done. Only create subagents for "+
			"genuinely independent work that benefits from its own "+
			"context or role; each one occupies a session and spends "+
			"tokens independently, and simple tasks should be done "+
			"directly. The agent persists under "+
			"~/.opencraft/agents/<name>/ and is re-registered on the "+
			"next start until unregistered.",
		message.ToolProperty("name", "string",
			"Agent id: lowercase letters, digits, and hyphens only (required)."),
		message.ToolProperty("description", "string",
			"One-sentence capability summary shown in delegation targets (required)."),
		message.ToolProperty("graph", "string",
			"Complete flowcraft graph definition (JSON or YAML) including "+
				"the system prompt; see the flowcraft-config skill for the format (required)."),
	).Required("name", "description", "graph").
		DisallowAdditionalProperties().Build()
}

func (createTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t createTool) Execute(
	ctx context.Context, arguments string,
) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Graph       string `json:"graph"`
	}
	if err := strictDecode(arguments, &args); err != nil {
		return "", err
	}
	ok, err := confirm.Confirm(ctx, "Create subagent?",
		fmt.Sprintf("Create persistent subagent %q (\"%s\")? Its graph "+
			"definition persists under ~/.opencraft/agents and is "+
			"re-registered on every startup.", args.Name, args.Description))
	if err != nil {
		return "", err
	}
	if !ok {
		return `{"cancelled":true,"action":"create"}`, nil
	}
	result, err := t.lifecycle.Create(ctx, AgentSpec{
		Name:        args.Name,
		Description: args.Description,
		Graph:       args.Graph,
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"name":         result.Name,
		"description":  result.Description,
		"persisted_to": result.PersistedTo,
		"created_at":   result.CreatedAt,
		"status":       "registered",
		"hint": "Now delegate work to it with the delegate tool " +
			"(target \"" + result.Name + "\"); check delegation_status; " +
			"unregister_agent when the subtask is done.",
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", CreateName, err)
	}
	return string(payload), nil
}

type updateTool struct{ lifecycle *Lifecycle }

var _ tool.Tool = updateTool{}

func (updateTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		UpdateName,
		"Updates an existing persistent subagent created by create_agent: "+
			"only the provided fields (description, graph) are replaced, "+
			"and the name is immutable (it identifies the agent; renaming "+
			"means unregister_agent then create_agent). The live "+
			"registration is swapped after in-flight delegations drain "+
			"(bounded by the remove timeout), so delegate calls may fail "+
			"briefly during the swap, and the "+
			"~/.opencraft/agents/<name>/agent.yaml declaration is then "+
			"rewritten. On any failure the previous definition stays in "+
			"effect; a call that changes nothing is a no-op. Author graph "+
			"changes with the flowcraft-config skill (install it first "+
			"with skill_install flowcraft-config if unavailable), then "+
			"call this tool. Workflow: create_agent → delegate → "+
			"update_agent to iterate on the definition → unregister_agent "+
			"when done.",
		message.ToolProperty("name", "string",
			"Agent id to update (required)."),
		message.ToolProperty("description", "string",
			"New one-sentence capability summary shown in delegation targets (optional; replaces the existing one)."),
		message.ToolProperty("graph", "string",
			"New complete flowcraft graph definition (JSON or YAML) including the system prompt (optional; replaces the existing graph)."),
	).Required("name").DisallowAdditionalProperties().Build()
}

func (updateTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t updateTool) Execute(
	ctx context.Context, arguments string,
) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Graph       string `json:"graph"`
	}
	if err := strictDecode(arguments, &args); err != nil {
		return "", err
	}
	ok, err := confirm.Confirm(ctx, "Update subagent?",
		fmt.Sprintf("Update persistent subagent %q? Its description and/or "+
			"graph definition will be replaced.", args.Name))
	if err != nil {
		return "", err
	}
	if !ok {
		return `{"cancelled":true,"action":"update"}`, nil
	}
	result, err := t.lifecycle.Update(ctx, args.Name, args.Description, args.Graph)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"name":         result.Name,
		"description":  result.Description,
		"persisted_to": result.PersistedTo,
		"created_at":   result.CreatedAt,
		"status":       "updated",
		"hint": "The updated definition is live; future delegate calls " +
			"target it.",
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", UpdateName, err)
	}
	return string(payload), nil
}

type removeTool struct{ lifecycle *Lifecycle }

var _ tool.Tool = removeTool{}

func (removeTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		RemoveName,
		"Removes a persistent subagent created by create_agent: in-flight "+
			"delegations drain first, new delegations fail with "+
			"TargetNotFound, and the agent's ~/.opencraft/agents/<name>/ "+
			"declaration is deleted so it does not return on the next "+
			"start. Deployed agents cannot be removed. Use it when the "+
			"subagent's work is complete or it is no longer useful.",
		message.ToolProperty("name", "string",
			"Agent id to remove (required)."),
	).Required("name").DisallowAdditionalProperties().Build()
}

func (removeTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t removeTool) Execute(
	ctx context.Context, arguments string,
) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictDecode(arguments, &args); err != nil {
		return "", err
	}
	ok, err := confirm.Confirm(ctx, "Remove subagent?",
		fmt.Sprintf("Remove persistent subagent %q? In-flight delegations "+
			"drain and its declaration under ~/.opencraft/agents is deleted.",
			args.Name))
	if err != nil {
		return "", err
	}
	if !ok {
		return `{"cancelled":true,"action":"remove"}`, nil
	}
	if err := t.lifecycle.Remove(ctx, args.Name); err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"name":   args.Name,
		"status": "unregistered",
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", RemoveName, err)
	}
	return string(payload), nil
}

// strictDecode decodes tool arguments, rejecting unknown fields.
func strictDecode(arguments string, into any) error {
	dec := json.NewDecoder(strings.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return errdefs.Validationf("agents tool: parse arguments: %v", err)
	}
	return nil
}
