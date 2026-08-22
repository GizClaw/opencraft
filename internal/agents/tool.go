package agents

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
)

const (
	// CreateName is the canonical create_agent tool name.
	CreateName = "create_agent"
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
		removeTool{t.lifecycle},
	}
}

type createTool struct{ lifecycle *Lifecycle }

var _ tool.Tool = createTool{}

func (createTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		CreateName,
		"Creates a persistent subagent for a focused subtask and makes "+
			"it available as a delegation target. The subagent runs on "+
			"the same flowcraft graph engine as the main agent (the "+
			"compact→llm→tools loop) with your instructions as its "+
			"system prompt. Workflow: create_agent → delegate to the "+
			"new name → delegation_status → unregister_agent when done. "+
			"Only create subagents for genuinely independent work that "+
			"benefits from its own context or role; each one occupies a "+
			"session and spends tokens independently, and simple tasks "+
			"should be done directly. The agent persists under "+
			"~/.opencraft/agents/<name>/ and is re-registered on the "+
			"next start until unregistered.",
		message.ToolProperty("name", "string",
			"Agent id: lowercase letters, digits, and hyphens only (required)."),
		message.ToolProperty("description", "string",
			"One-sentence capability summary shown in delegation targets (required)."),
		message.ToolProperty("instructions", "string",
			"System prompt for the subagent: its role, the concrete task "+
				"boundary, constraints, and how to report back (required)."),
		message.ToolProperty("model", "string",
			"Optional model as \"provider/name\"; empty routes through the router."),
		message.ToolProperty("think_level", "string",
			"Optional reasoning effort: low | medium | high (default medium)."),
		message.ToolProperty("tools", "string",
			"Optional tool surface: all (read, edit, commands; default) or "+
				"read_only (read_file, grep, glob, list_dir, web_fetch)."),
	).Required("name", "description", "instructions").
		DisallowAdditionalProperties().Build()
}

func (createTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t createTool) Execute(
	ctx context.Context, arguments string,
) (string, error) {
	var args struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
		Model        string `json:"model"`
		ThinkLevel   string `json:"think_level"`
		Tools        string `json:"tools"`
	}
	if err := strictDecode(arguments, &args); err != nil {
		return "", err
	}
	result, err := t.lifecycle.Create(ctx, AgentSpec{
		Name:         args.Name,
		Description:  args.Description,
		Instructions: args.Instructions,
		Model:        args.Model,
		ThinkLevel:   args.ThinkLevel,
		Tools:        ToolsMode(args.Tools),
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
