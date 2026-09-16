// Package exec provides the exec_command and exec_session tools, both
// backed by a sandbox.Runner: exec_command runs simple commands
// directly in the sandbox (no shell wrapper), and commands needing
// shell features run through /bin/sh -c; exec_session manages
// long-running sessions.
package exec

import (
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/foundation/utils/shelldetect"
)

// CommandName is the canonical exec_command tool name.
const CommandName = "exec_command"

// CommandTool runs a command string either as direct argv (simple
// commands) or through the platform shell (commands with shell
// syntax); see shelldetect.Spec.
type CommandTool struct {
	runner sandbox.Runner
	shell  shelldetect.Spec
	// escalator asks the user to widen the boundary after the OS
	// sandbox refused a command. Nil keeps refusals as plain command
	// failures (headless runs, embedded hosts, tests).
	escalator ocsandbox.Escalator
}

// NewCommand creates the exec_command tool. runner is required.
func NewCommand(
	runner sandbox.Runner, opts ...CommandOption,
) (*CommandTool, error) {
	if runner == nil {
		return nil, errInvalid("runner is required")
	}
	t := &CommandTool{
		runner: runner,
		shell:  shelldetect.Detect(goruntime.GOOS),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t, nil
}

// CommandOption configures an exec tool at construction.
type CommandOption func(*CommandTool)

// WithEscalator enables the post-denial escalation prompt: when the OS
// sandbox refuses a command, the escalator asks the user whether it may
// run on the host instead, and an approval re-runs it unconfined.
func WithEscalator(e ocsandbox.Escalator) CommandOption {
	return func(t *CommandTool) { t.escalator = e }
}

// WithShell overrides the platform shell used for commands that need
// shell syntax. An empty Program leaves the platform default in place.
// The wiring passes the deployment's target OS so cross-compiled
// builds and tests do not depend on the host.
func WithShell(spec shelldetect.Spec) CommandOption {
	return func(t *CommandTool) {
		if spec.Program == "" {
			return
		}
		t.shell = spec
	}
}

// MustNewCommand panics on invalid construction; use in static wiring.
func MustNewCommand(
	runner sandbox.Runner, opts ...CommandOption,
) *CommandTool {
	t, err := NewCommand(runner, opts...)
	if err != nil {
		panic(err)
	}
	return t
}

// Definition implements tool.Tool.
func (t *CommandTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		CommandName,
		"Run a command inside the agent's sandbox. Simple commands "+
			"(a bare program with plain arguments, no shell syntax) are "+
			"executed directly; commands needing shell features "+
			"(pipelines, redirects, && chains, env vars, globs) run "+
			"through "+t.shell.CommandLine()+" and may require user "+
			"approval. "+
			"Prefer simple commands when possible. Returns exit_code, "+
			"stdout, and stderr as JSON; a non-zero exit_code is "+
			"reported in the result body, not as an error. When the "+
			"sandbox refused the command, the user may be asked whether "+
			"it may run outside the sandbox; an approved retry adds a "+
			"note field saying so. Quote any argument that contains a "+
			"space (for example a Windows path), since arguments are "+
			"split on whitespace.",
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
func (t *CommandTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *CommandTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *CommandTool) execute(ctx context.Context, arguments string) (string, error) {
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
	argv, direct := directArgs(args.Command, t.shell.SafeRunes)
	if !direct {
		argv = append(
			append([]string{t.shell.Program}, t.shell.Args...),
			args.Command,
		)
	}
	run := func(runCtx context.Context) (*sandbox.ExecResult, error) {
		return sandbox.Exec(runCtx, t.runner, argv[0], argv[1:], opts)
	}
	result, err := run(ctx)
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
	result, note, err := t.maybeEscalate(ctx, run, argv, args.Command, result)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"exit_code": result.ExitCode,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
	}
	if note != "" {
		body["note"] = note
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", errdefs.Internalf("exec_command: encode result: %v", err)
	}
	return string(payload), nil
}

// maybeEscalate turns one OS-sandbox refusal into an explicit question
// and, when the user approves, re-runs the same command outside the
// sandbox. It returns the result the caller should report plus the
// note to attach ("" when the confined result stands as-is).
//
// The re-run repeats the whole command, so the prompt says so: a
// command that failed halfway may already have had side effects.
func (t *CommandTool) maybeEscalate(
	ctx context.Context,
	run func(context.Context) (*sandbox.ExecResult, error),
	argv []string,
	command string,
	result *sandbox.ExecResult,
) (*sandbox.ExecResult, string, error) {
	reason, denied := ocsandbox.Denied(result)
	if !denied || t.escalator == nil || !t.escalationAvailable(ctx) {
		return result, "", nil
	}
	decision, err := t.escalator.Escalate(ctx, ocsandbox.EscalationRequest{
		Command: command,
		Rule:    ocsandbox.RuleFor(argv),
		Reason:  reason,
		Detail:  ocsandbox.DenialExcerpt(result),
	})
	if err != nil {
		// Fail closed: the refusal stands unless the user could be
		// asked. The original result is still the truth on the ground.
		telemetry.WarnErr(ctx,
			"exec_command: escalation prompt failed; keeping the sandboxed result",
			err)
		return result, "", nil
	}
	if !decision.Allow {
		return result, "", nil
	}
	retried, err := run(ocsandbox.WithEscalation(ctx))
	if err != nil {
		// The user approved the retry, so a failure to even start it
		// is the result the caller needs to see.
		return nil, "", err
	}
	note := "the sandbox refused this command; the user approved " +
		"running it outside the sandbox"
	if decision.Remember {
		note += " (remembered for this command)"
	}
	return retried, note, nil
}

// escalationAvailable reports whether the wired runner says an
// approved retry would actually change the confine. Read-only and
// unconfined sessions answer false.
func (t *CommandTool) escalationAvailable(ctx context.Context) bool {
	gate, ok := t.runner.(ocsandbox.EscalationGate)
	return ok && gate.EscalationAvailable(ctx)
}

// directArgs reports whether command is a simple invocation that can
// run without a shell, returning its argv. Any shell metacharacter,
// quote, env assignment, or glob forces the shell path instead, so
// execution semantics never change silently. extraSafe carries the
// platform's unambiguous punctuation (see ShellSpec.SafeRunes).
func directArgs(command, extraSafe string) ([]string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	// A command whose whitespace does not round-trip through Fields is
	// not a plain invocation (repeated spaces, tabs): hand it to the
	// shell rather than guessing where the arguments were meant to
	// split. Note this cannot see "C:\Program Files\x.exe", which every
	// Windows shell also reads as two tokens unless it is quoted — the
	// tool description asks for quoting instead of guessing.
	if strings.Join(fields, " ") != command {
		return nil, false
	}
	for _, f := range fields {
		if !safeWord(f, extraSafe) {
			return nil, false
		}
	}
	return fields, true
}

func safeWord(s, extraSafe string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
		case strings.ContainsRune("_./:+-", r):
		case extraSafe != "" && strings.ContainsRune(extraSafe, r):
		default:
			return false
		}
	}
	return true
}

// Compile-time assertion.
var _ tool.Tool = (*CommandTool)(nil)

func errInvalid(msg string) error {
	return fmt.Errorf("exec_command: %s", msg)
}
