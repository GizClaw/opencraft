package applypatch

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/utils/patch"
)

// Name is the canonical apply_patch tool name.
const Name = "apply_patch"

// Tool is the LLM-callable apply_patch tool. It applies codex-style
// patches through a workspace.
type Tool struct {
	ws workspace.Workspace
}

// New creates the apply_patch tool. ws is required.
func New(ws workspace.Workspace) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf(
			"apply_patch: workspace is required")
	}
	return &Tool{ws: ws}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(ws workspace.Workspace) *Tool {
	t, err := New(ws)
	if err != nil {
		panic(err)
	}
	return t
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Apply a patch to files in the workspace. The patch uses the "+
			"codex format: *** Begin Patch / *** Add File / *** Update File "+
			"/ *** Delete File / *** End Patch. Paths are relative to the "+
			"workspace root; absolute paths and .. are rejected. Returns "+
			"the list of changed files.",
		message.ToolProperty("patch", "string",
			"The patch text to apply (required)."),
	).Required("patch").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("apply_patch: parse arguments: %v", err)
	}
	ops, err := patch.Parse(args.Patch)
	if err != nil {
		return "", err
	}
	results, err := patch.Apply(ctx, t.ws, ops)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{"files": results})
	if err != nil {
		return "", errdefs.Internalf("apply_patch: encode result: %v", err)
	}
	return string(payload), nil
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)
