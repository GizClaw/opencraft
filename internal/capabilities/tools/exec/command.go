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
	"github.com/GizClaw/flowcraft/core/tool"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/toolargs"
	"github.com/GizClaw/opencraft/internal/foundation/platform/shelldetect"
)

// CommandName is the canonical exec_command tool name.
const CommandName = "exec_command"

// commandAliases maps the argument name several other harnesses use
// for a command line onto this tool's canonical "command". The alias
// stays undocumented in the schema, like apply_patch's "input": it
// exists so a model habit costs nothing to the model, not as a second
// public name for the argument.
var commandAliases = map[string]string{"cmd": "command"}

// CommandTool runs a command string either as direct argv (simple
// commands) or through the platform shell (commands with shell
// syntax); see shelldetect.Spec.
type CommandTool struct {
	runner sandbox.Runner
	shell  shelldetect.Spec
	// escalation is the runner's escalation front end: the confined
	// attempt, the refusal detection, the one prompt and the approved
	// retry through the host chain all live in it (flowcraft core). Nil
	// keeps refusals as plain command failures (headless runs, embedded
	// hosts, tests, a deployment that wired no escalator).
	escalation *sandbox.Escalation
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
	if provider, ok := runner.(ocsandbox.EscalationProvider); ok {
		t.escalation = provider.Escalation()
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
	if err := toolargs.Decode(CommandName, arguments, commandAliases, &args); err != nil {
		return "", err
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
	result, note, err := t.run(ctx, argv, opts)
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

// run executes one command line: through the escalation front end when
// the runner serves one (a refusal becomes a question, and an approval
// re-runs the command on the host), through the plain runner otherwise.
// The note is non-empty only when an approval changed where the command
// ran.
func (t *CommandTool) run(
	ctx context.Context, argv []string, opts sandbox.ExecOptions,
) (*sandbox.ExecResult, string, error) {
	if t.escalation == nil {
		res, err := sandbox.Exec(ctx, t.runner, argv[0], argv[1:], opts)
		return res, "", err
	}
	res, outcome, err := t.escalation.Exec(ctx, argv[0], argv[1:], opts)
	if err != nil {
		return res, "", err
	}
	return res, escalationNote(outcome), nil
}

// escalationNote renders what the model should know about a retry: a
// refusal the user overrode, a rule that skipped the confined attempt,
// or nothing when the confined result stands as it is.
func escalationNote(outcome sandbox.EscalationOutcome) string {
	switch {
	case !outcome.Approved:
		return ""
	case outcome.Remembered && !outcome.Refused:
		// A remembered rule covered this command, so it never ran
		// confined and nothing was refused.
		return "a remembered approval ran this command outside the sandbox"
	default:
		note := "the sandbox refused this command; the user approved " +
			"running it outside the sandbox"
		if outcome.Remembered {
			note += " (remembered for this command)"
		}
		return note
	}
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
