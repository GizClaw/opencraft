package sandbox

import (
	"context"
	"strings"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// This file owns the per-command escalation path: the host-side
// detection of an OS sandbox refusal, the request the exec tool hands
// to the user, and the context marker that lets one approved retry run
// outside the sandbox.
//
// The approval gate (flowcraft's WithApproval) decides whether a
// command may *run*; it deliberately never widens the policy the
// command runs under, so a command that is allowed but then refused by
// seatbelt/bwrap fails like any other command. Escalation is the
// second, explicit question for exactly that case: the user is asked
// whether this one command may leave the confine, and the answer is an
// ordinary approval decision the runner can act on.

// EscalationRequest describes one confined attempt the OS sandbox
// refused. It is rendered into the user prompt; Command is the command
// line as the model wrote it (not the argv the runner spawned).
type EscalationRequest struct {
	// Command is what the user sees; Rule is the normalized token
	// prefix persisted for a "remember" answer. They differ for
	// shell-wrapped calls ("sh -c 'pip install x'" vs "pip install x").
	Command string
	Rule    string
	Reason  string
	Detail  string
}

// EscalationDecision is the user's answer to an EscalationRequest.
// Remember persists an "always run this command without the sandbox"
// rule for the workspace.
type EscalationDecision struct {
	Allow    bool
	Remember bool
}

// Escalator asks the user to widen the boundary for one refused
// command. The exec policy manager implements it; without a wired
// escalator (headless, tests) a refusal stays a plain command failure.
type Escalator interface {
	Escalate(
		ctx context.Context, req EscalationRequest,
	) (EscalationDecision, error)
}

// EscalationRules is the read side of the persisted escalation rules.
// The runner consults it before starting a command so a remembered
// rule skips the confined attempt (and its doomed first failure)
// entirely.
type EscalationRules interface {
	EscalatedAllowed(req coresandbox.ExecRequest) bool
}

// EscalationGate is implemented by runners that can offer the
// escalation retry at all. It is a separate interface so tools keep
// depending on the plain sandbox.Runner.
type EscalationGate interface {
	// EscalationAvailable reports whether an approved retry would
	// actually change the confine. YOLO sessions are already
	// unconfined, and read-only sessions must not trade their
	// workspace read-only guarantee for a per-command approval.
	EscalationAvailable(ctx context.Context) bool
}

// escalationKey marks a context whose command was explicitly approved
// to run outside the sandbox for one retry.
type escalationKey struct{}

// WithEscalation marks ctx so the next Start on a HostSandbox skips the
// confined chain. It carries no approval of its own: callers must have
// obtained one, and it never weakens a read-only session (HostSandbox
// still refuses to escalate those).
func WithEscalation(ctx context.Context) context.Context {
	return context.WithValue(ctx, escalationKey{}, true)
}

// Escalating reports whether ctx carries an approved escalation.
func Escalating(ctx context.Context) bool {
	marked, _ := ctx.Value(escalationKey{}).(bool)
	return marked
}

// RuleFor renders one spawn's argv as the rule string the approval and
// escalation stores match against: shell wrappers ("sh -c …") are
// unwrapped so a remembered rule describes what the user actually
// asked to run, exactly like the allowlist path does.
func RuleFor(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return strings.Join(coresandbox.NormaliseExec(coresandbox.ExecRequest{
		Command: argv[0],
		Args:    argv[1:],
	}), " ")
}

// denialMarkers are the stderr fragments the OS backends and the
// shells in front of them leave behind when a syscall is refused.
// macOS seatbelt denies a write with EPERM ("Operation not permitted"),
// bwrap leaves the rest of the filesystem on a read-only bind (EROFS),
// and the Windows write-confinement token fails with access denied —
// phrased differently by each shell: cmd says "Access is denied", while
// PowerShell reports the .NET error ("Access to the path 'X' is denied",
// "UnauthorizedAccessException", "PermissionDenied").
//
// "permission denied" is deliberately absent: it is the ordinary
// EACCES of unrelated file-mode problems, and prompting to leave the
// sandbox for those would train users to approve noise.
var denialMarkers = []string{
	"operation not permitted",
	"read-only file system",
	"access is denied",
	"access to the path",
	"unauthorizedaccessexception",
	"permissiondenied",
	"deny file-write",
}

// quickRejectExitCodes are the conventional "cannot execute" statuses
// (not executable, command not found). They never mean the sandbox
// refused a syscall.
//
// Exit code 2 is deliberately absent even though it is the other
// conventional usage-error status: dash returns 2 when a redirection
// fails, which is exactly what a refused write looks like
// ("/bin/sh: 1: cannot create …: Read-only file system"). The marker
// text, not the exit code, is what separates a refusal from an ordinary
// failure.
var quickRejectExitCodes = map[int]bool{126: true, 127: true}

// Denied reports whether res looks like the OS sandbox refused the
// command, with a short reason for the user prompt.
//
// Like every command predicate in this codebase this is a tripwire,
// not a wall: a false positive only produces a prompt the user can
// decline, and the OS backend stays the enforcement point. It exists
// because the sandbox is otherwise indistinguishable from a command
// that genuinely lacked permission.
func Denied(res *coresandbox.ExecResult) (string, bool) {
	if res == nil || res.ExitCode == 0 || quickRejectExitCodes[res.ExitCode] {
		return "", false
	}
	haystack := strings.ToLower(res.Stderr)
	if haystack == "" {
		haystack = strings.ToLower(res.Stdout)
	}
	for _, marker := range denialMarkers {
		if !strings.Contains(haystack, marker) {
			continue
		}
		return "the sandbox refused the command (" + marker + ")", true
	}
	return "", false
}

// DenialExcerpt picks the stderr line that carries the refusal, so the
// escalation prompt can show the concrete error instead of the whole
// captured output. It falls back to the first non-empty stderr line.
func DenialExcerpt(res *coresandbox.ExecResult) string {
	const maxLen = 400
	if res == nil {
		return ""
	}
	for _, line := range strings.Split(res.Stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !isDenialLine(line) {
			continue
		}
		return truncateLine(line, maxLen)
	}
	for _, line := range strings.Split(res.Stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateLine(line, maxLen)
		}
	}
	return ""
}

func isDenialLine(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range denialMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func truncateLine(line string, maxLen int) string {
	if len(line) <= maxLen {
		return line
	}
	return line[:maxLen] + "…"
}
