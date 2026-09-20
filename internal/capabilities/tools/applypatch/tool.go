package applypatch

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/foundation/utils/patch"
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

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *Tool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// args is the decoded apply_patch tool input.
type args struct {
	Patch string `json:"patch"`
	// Input is an accepted alias for Patch. Several model families
	// were trained on apply_patch harnesses that pass the patch text
	// as "input", and the codex patch text is identical either way.
	// It stays undocumented in the tool schema: Patch is canonical.
	Input string `json:"input"`
}

// decodeArgs parses the tool arguments, rejecting unknown keys. A
// provider-specific shape must fail by naming the offending key
// instead of silently decoding to an empty patch.
func decodeArgs(arguments string) (args, error) {
	var out args
	dec := json.NewDecoder(strings.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return args{}, errdefs.Validationf(
			"apply_patch: parse arguments: %v", err)
	}
	return out, nil
}

// execute renders the tool's text result.
func (t *Tool) execute(ctx context.Context, arguments string) (string, error) {
	parsed, err := decodeArgs(arguments)
	if err != nil {
		return "", err
	}
	text := parsed.Patch
	if strings.TrimSpace(text) == "" {
		text = parsed.Input
	}
	if strings.TrimSpace(text) == "" {
		return "", errdefs.Validationf(
			"apply_patch: missing required argument %q", "patch")
	}
	// ParseAny keeps the codex envelope as the documented input while
	// accepting a standard unified diff too: a patch copied from
	// `git diff` applies with the same validation and the same
	// content-matched hunks.
	ops, err := patch.ParseAny(text)
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
