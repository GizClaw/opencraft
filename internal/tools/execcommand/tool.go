// Package execcommand provides the exec_command tool: simple commands
// run directly in the sandbox (no shell wrapper), and commands needing
// shell features run through /bin/sh -c.
package execcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"
)

// Name is the canonical exec_command tool name.
const Name = "exec_command"

// Tool runs a shell command string via /bin/sh -c.
type Tool struct {
	runner sandbox.Runner
}

// New creates the exec_command tool. runner is required.
func New(runner sandbox.Runner) (*Tool, error) {
	if runner == nil {
		return nil, errInvalid("runner is required")
	}
	return &Tool{runner: runner}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(runner sandbox.Runner) *Tool {
	t, err := New(runner)
	if err != nil {
		panic(err)
	}
	return t
}

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Run a command inside the agent's sandbox. Simple commands "+
			"(a bare program with plain arguments, no shell syntax) are "+
			"executed directly; commands needing shell features "+
			"(pipelines, redirects, && chains, env vars, globs) run "+
			"through /bin/sh -c and may require user approval. "+
			"Prefer simple commands when possible. Returns exit_code, "+
			"stdout, and stderr as JSON; a non-zero exit_code is "+
			"reported in the result body, not as an error.",
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

	var timeout time.Duration
	if args.TimeoutSeconds != nil && *args.TimeoutSeconds > 0 {
		timeout = time.Duration(*args.TimeoutSeconds * float64(time.Second))
	}
	opts := sandbox.ExecOptions{
		WorkDir: args.Workdir,
		Stdin:   []byte(args.Stdin),
		Timeout: timeout,
	}
	var result *sandbox.ExecResult
	var err error
	if argv, ok := directArgs(args.Command); ok {
		result, err = sandbox.Exec(ctx, t.runner, argv[0], argv[1:], opts)
	} else {
		result, err = sandbox.Exec(ctx, t.runner, "/bin/sh",
			[]string{"-c", args.Command}, opts)
	}
	if err != nil {
		if errdefs.IsPolicyDenied(err) {
			// The sandbox's denial message names the raw argv (e.g.
			// "/bin/sh" for shell-wrapped commands); the tool knows the
			// actual command string, so surface that first.
			return "", errdefs.PolicyDeniedf(
				"exec_command: command %q denied by sandbox policy: %v",
				args.Command, err)
		}
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

// directArgs reports whether command is a simple invocation that can
// run without a shell, returning its argv. Any shell metacharacter,
// quote, env assignment, or glob forces the shell path instead, so
// execution semantics never change silently.
func directArgs(command string) ([]string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	for _, f := range fields {
		if !safeWord(f) {
			return nil, false
		}
	}
	return fields, true
}

func safeWord(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
		case strings.ContainsRune("_./:+-", r):
		default:
			return false
		}
	}
	return true
}

// Compile-time assertion.
var _ tool.Tool = (*Tool)(nil)

func errInvalid(msg string) error {
	return fmt.Errorf("exec_command: %s", msg)
}
