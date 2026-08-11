// Package execcommand provides the shell-semantics exec_command tool:
// it runs the model-provided command string through /bin/sh -c in an
// execd.Environment (by default the remote execd), so
// pipelines, redirects, and && chains work like a normal shell.
package execcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/flowcraft/sdk/message"
	"github.com/GizClaw/flowcraft/sdk/tool"
	"github.com/GizClaw/opencraft/internal/execd"
)

// Name is the canonical exec_command tool name.
const Name = "exec_command"

// Tool runs a shell command string via /bin/sh -c.
type Tool struct {
	env execd.Environment
}

// New creates the exec_command tool. env is required.
func New(env execd.Environment) (*Tool, error) {
	if env == nil {
		return nil, errInvalid("environment is required")
	}
	return &Tool{env: env}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(env execd.Environment) *Tool {
	t, err := New(env)
	if err != nil {
		panic(err)
	}
	return t
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.Definition {
	return message.DefineSchema(
		Name,
		"Run a shell command string inside the agent's sandbox. "+
			"The command is executed with /bin/sh -c, so pipelines, "+
			"redirects, && chains, and shell builtins work. Returns "+
			"exit_code, stdout, and stderr as JSON; a non-zero exit_code "+
			"is reported in the result body, not as an error.",
		message.ToolProperty("command", "string",
			"The shell command line to run (required), e.g. "+
				"\"rg --files internal | rg httpclient\"."),
		message.ToolProperty("workdir", "string",
			"Working directory, relative to the sandbox root. Empty means the sandbox root itself."),
		message.ToolProperty("stdin", "string",
			"Bytes piped to the command's stdin. Omit when the command does not read stdin."),
		message.ToolProperty("timeout_seconds", "number",
			"Per-call timeout in seconds. Zero or negative disables the tool-level timeout."),
	).Required("command").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Command        string   `json:"command"`
		Workdir        string   `json:"workdir"`
		Stdin          string   `json:"stdin"`
		TimeoutSeconds *float64 `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("exec_command: parse arguments: %v", err)
	}
	if args.Command == "" {
		return "", errInvalid("command is required")
	}

	req := execd.Request{
		Argv:    []string{"/bin/sh", "-c", args.Command},
		WorkDir: args.Workdir,
		Stdin:   []byte(args.Stdin),
	}
	if args.TimeoutSeconds != nil && *args.TimeoutSeconds > 0 {
		req.Timeout = time.Duration(*args.TimeoutSeconds * float64(time.Second))
	}

	result, err := t.env.Exec(ctx, req)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"exit_code": result.ExitCode,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
	})
	if err != nil {
		return "", errdefs.Internalf("exec_command: encode result: %v", err)
	}
	return string(payload), nil
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)

func errInvalid(msg string) error {
	return fmt.Errorf("exec_command: %s", msg)
}
