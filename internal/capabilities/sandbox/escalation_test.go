package sandbox

import (
	"context"
	"testing"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestDeniedRecognizesSandboxRefusals(t *testing.T) {
	cases := []struct {
		name    string
		result  coresandbox.ExecResult
		want    bool
		marker  string
		comment string
	}{
		{
			name:   "seatbelt eperm",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "python: PermissionError: [Errno 1] Operation not permitted: '/Users/x/.local/lib'"},
			want:   true,
		},
		{
			name:   "bwrap read-only bind",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "pip: error: could not write to '/home/x/.local': Read-only file system"},
			want:   true,
		},
		{
			name:   "windows write confinement",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "Access is denied."},
			want:   true,
		},
		{
			name: "powershell dotnet denial",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "Set-Content: " +
				"Access to the path '/home/x/.local/lib' is denied."},
			want: true,
		},
		{
			name: "powershell exception name",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "Exception: " +
				"System.UnauthorizedAccessException: Access is denied"},
			want: true,
		},
		{
			name:   "powershell error category",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "[PermissionDenied]"},
			want:   true,
		},
		{
			name:   "seatbelt profile text",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "deny file-write-create /Users/x/.local"},
			want:   true,
		},
		{
			name:   "stdout fallback",
			result: coresandbox.ExecResult{ExitCode: 1, Stdout: "Operation not permitted"},
			want:   true,
		},
		{
			name:   "success",
			result: coresandbox.ExecResult{ExitCode: 0, Stderr: "Operation not permitted"},
			want:   false,
		},
		{
			name:   "command not found",
			result: coresandbox.ExecResult{ExitCode: 127, Stderr: "sh: nonexistent: Operation not permitted"},
			want:   false,
		},
		{
			name: "dash redirection failure",
			result: coresandbox.ExecResult{ExitCode: 2, Stderr: "/bin/sh: 1: " +
				"cannot create /var/tmp/x.txt: Read-only file system"},
			want: true,
		},
		{
			name:   "not executable",
			result: coresandbox.ExecResult{ExitCode: 126, Stderr: "Operation not permitted"},
			want:   false,
		},
		{
			name:    "plain eacces stays quiet",
			result:  coresandbox.ExecResult{ExitCode: 1, Stderr: "cat: /etc/shadow: Permission denied"},
			want:    false,
			comment: "ordinary file-mode failures must not prompt",
		},
		{
			name:   "compile failure",
			result: coresandbox.ExecResult{ExitCode: 1, Stderr: "./main.go:12:2: undefined: foo"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, got := Denied(&tc.result)
			if got != tc.want {
				t.Fatalf("Denied = %v (%q), want %v", got, reason, tc.want)
			}
			if tc.want && reason == "" {
				t.Fatal("denial without a reason")
			}
		})
	}
}

func TestDeniedNilResult(t *testing.T) {
	if _, ok := Denied(nil); ok {
		t.Fatal("nil result must not look denied")
	}
}

func TestEscalationDetailPicksDenialLine(t *testing.T) {
	res := &coresandbox.ExecResult{
		ExitCode: 1,
		Stderr: "Collecting requests\n" +
			"ERROR: Could not install packages: [Errno 1] Operation not permitted: '/Users/x/.local/lib/python3.13/site-packages'\n" +
			"See https://pip.pypa.io for help\n",
	}
	detail := DenialExcerpt(res)
	if want := "ERROR: Could not install packages"; len(detail) < len(want) ||
		detail[:len(want)] != want {
		t.Fatalf("detail = %q, want the denial line", detail)
	}
}

func TestRuleForUnwrapsShell(t *testing.T) {
	got := RuleFor([]string{"/bin/sh", "-c", "pip install requests"})
	if got != "pip install requests" {
		t.Fatalf("rule = %q", got)
	}
	if got := RuleFor(nil); got != "" {
		t.Fatalf("empty argv rule = %q", got)
	}
}

// stubEscalationRules is the read side of the persisted escalation
// rules: it matches whatever the test registered.
type stubEscalationRules struct {
	rules []string
}

func (s stubEscalationRules) EscalatedAllowed(req coresandbox.ExecRequest) bool {
	got := RuleFor(append([]string{req.Command}, req.Args...))
	for _, rule := range s.rules {
		if rule == got {
			return true
		}
	}
	return false
}

func TestHostSandboxEscalationRouting(t *testing.T) {
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	confined := &captureOptsRunner{}
	unconfined := &recordRunner{}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
		escalation: stubEscalationRules{rules: []string{"pip install requests"}},
	}
	spec := coresandbox.SessionSpec{
		ID:   "p1",
		Argv: []string{"/bin/sh", "-c", "pip install requests"},
	}

	// No marker, no matching rule: confined.
	if _, err := hs.Start(
		sessionCtx(id), coresandbox.SessionSpec{
			ID: "p0", Argv: []string{"go", "test", "./..."},
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(confined.got) != 1 || unconfined.count() != 0 {
		t.Fatalf("unmatched command routed wrong: confined=%d unconfined=%d",
			len(confined.got), unconfined.count())
	}

	// Remembered rule: straight to the unconfined runner.
	if _, err := hs.Start(sessionCtx(id), spec); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 1 {
		t.Fatalf("remembered rule must skip the confined attempt")
	}

	// Approved escalation marker: same bypass for one call.
	if _, err := hs.Start(
		WithEscalation(sessionCtx(id)), coresandbox.SessionSpec{
			ID: "p2", Argv: []string{"npm", "install", "left-pad"},
		},
	); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 2 {
		t.Fatalf("approved escalation must use the unconfined runner")
	}
}

// TestHostSandboxRememberedRuleSkipsTTY pins the blast radius of a
// remembered rule: it covers one-shot commands, never an interactive
// session started with the same argv.
func TestHostSandboxRememberedRuleSkipsTTY(t *testing.T) {
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	confined := &captureOptsRunner{}
	unconfined := &recordRunner{}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
		escalation: stubEscalationRules{rules: []string{"pip install requests"}},
	}
	if _, err := hs.Start(sessionCtx(id), coresandbox.SessionSpec{
		ID:   "tty",
		TTY:  true,
		Argv: []string{"/bin/sh", "-c", "pip install requests"},
	}); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 0 || len(confined.got) != 1 {
		t.Fatalf("TTY start escaped the confine: unconfined=%d confined=%d",
			unconfined.count(), len(confined.got))
	}
	// An explicit per-call approval still escalates (that is a fresh
	// user decision, not a remembered rule).
	if _, err := hs.Start(WithEscalation(sessionCtx(id)), coresandbox.SessionSpec{
		ID:   "tty-approved",
		TTY:  true,
		Argv: []string{"/bin/sh", "-c", "pip install requests"},
	}); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 1 {
		t.Fatalf("explicit approval must still escalate")
	}
}

func TestUnconfinedRequest(t *testing.T) {
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	ctx := sessionCtx(id)
	if unconfinedRequest(ctx, store) {
		t.Fatal("workspace session without a marker must stay confined")
	}
	if !unconfinedRequest(WithEscalation(ctx), store) {
		t.Fatal("approved escalation must request the unconfined runner")
	}
	if err := store.SetMode(
		context.Background(), id, sessions.ModeYOLO,
	); err != nil {
		t.Fatal(err)
	}
	if !unconfinedRequest(ctx, store) {
		t.Fatal("YOLO session must request the unconfined runner")
	}
}

func TestHostSandboxReadOnlyRefusesEscalation(t *testing.T) {
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMode(
		context.Background(), id, sessions.ModeReadOnly,
	); err != nil {
		t.Fatal(err)
	}
	confined := &captureOptsRunner{}
	unconfined := &recordRunner{}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
		escalation: stubEscalationRules{rules: []string{"pip install requests"}},
	}
	if _, err := hs.Start(
		WithEscalation(sessionCtx(id)), coresandbox.SessionSpec{
			ID: "ro", Argv: []string{"/bin/sh", "-c", "pip install requests"},
		},
	); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 0 {
		t.Fatal("read-only session must keep the confine")
	}
	if len(confined.got) != 1 ||
		confined.got[0].Write != coresandbox.WriteReadOnly {
		t.Fatalf("read-only opts = %+v", confined.got)
	}
	if hs.EscalationAvailable(sessionCtx(id)) {
		t.Fatal("read-only session must not advertise escalation")
	}
}

func TestHostSandboxEscalationAvailable(t *testing.T) {
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	hs := &HostSandbox{sessions: store}
	if !hs.EscalationAvailable(sessionCtx(id)) {
		t.Fatal("workspace mode should offer escalation")
	}
	if err := store.SetMode(
		context.Background(), id, sessions.ModeYOLO,
	); err != nil {
		t.Fatal(err)
	}
	if hs.EscalationAvailable(sessionCtx(id)) {
		t.Fatal("yolo sessions are already unconfined")
	}
}
