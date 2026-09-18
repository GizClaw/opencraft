package exec

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/foundation/utils/shelldetect"
)

type cmdRunner struct {
	started []sandbox.SessionSpec
	out     sandbox.SessionOutput
	exit    sandbox.SessionExit
	err     error
}

func (f *cmdRunner) Close() error { return nil }

func (f *cmdRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{}
}

func (f *cmdRunner) Start(
	_ context.Context,
	spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.started = append(f.started, spec)
	return &cmdSession{out: f.out, exit: f.exit}, nil
}

func (f *cmdRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}

func (f *cmdRunner) Terminate(context.Context, string) error { return nil }

type cmdSession struct {
	out  sandbox.SessionOutput
	exit sandbox.SessionExit
}

func (s *cmdSession) ID() string { return "s" }
func (s *cmdSession) PID() int   { return 1 }
func (s *cmdSession) Capabilities() sandbox.SessionCapabilities {
	return sandbox.SessionCapabilities{}
}
func (s *cmdSession) Read(
	context.Context, int64, int,
) (sandbox.SessionOutput, error) {
	return s.out, nil
}
func (s *cmdSession) Write(context.Context, []byte) error { return nil }
func (s *cmdSession) CloseInput() error                   { return nil }
func (s *cmdSession) Resize(context.Context, int, int) error {
	return nil
}
func (s *cmdSession) Signal(context.Context, sandbox.SessionSignal) error {
	return nil
}
func (s *cmdSession) Terminate(context.Context) error { return nil }
func (s *cmdSession) Wait(context.Context) (sandbox.SessionExit, error) {
	return s.exit, nil
}
func (s *cmdSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	return nil, nil
}
func (s *cmdSession) Close() error { return nil }

func newCommandTool() (*CommandTool, *cmdRunner) {
	runner := &cmdRunner{
		out: sandbox.SessionOutput{
			NextSeq: 1,
			Chunks: []sandbox.OutputChunk{{
				Seq:    0,
				Stream: sandbox.SessionStreamStdout,
				Data:   []byte("out"),
			}},
			EOF: true,
		},
		exit: sandbox.SessionExit{Code: 0, Reason: sandbox.SessionExited},
	}
	// Pin the POSIX shell so the assertions describe the repository's
	// default deployment rather than the machine running the test.
	tool, err := NewCommand(runner, WithShell(testPosixShell))
	if err != nil {
		panic(err)
	}
	return tool, runner
}

// testPosixShell is the shell a macOS/Linux deployment gets; windows
// and cross-compiled tests pass their own spec explicitly.
var testPosixShell = shelldetect.Default("darwin")

func TestExecuteRunsShellCommand(t *testing.T) {
	tool, runner := newCommandTool()
	out, err := tool.Execute(context.Background(),
		`{"command":"rg --files internal | rg httpclient"}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(out.Text()), &res); err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out" {
		t.Errorf("stdout = %q", res.Stdout)
	}
	want := []string{"/bin/sh", "-c", "rg --files internal | rg httpclient"}
	if !reflect.DeepEqual(runner.started[0].Argv, want) {
		t.Errorf("argv = %v", runner.started[0].Argv)
	}
}

func TestExecuteRunsSimpleCommandDirectly(t *testing.T) {
	tool, runner := newCommandTool()
	if _, err := tool.Execute(context.Background(),
		`{"command":"rg --files internal"}`); err != nil {
		t.Fatal(err)
	}
	want := []string{"rg", "--files", "internal"}
	if !reflect.DeepEqual(runner.started[0].Argv, want) {
		t.Errorf("argv = %v, want %v", runner.started[0].Argv, want)
	}
}

func TestDirectArgs(t *testing.T) {
	cases := []struct {
		cmd  string
		argv []string
		ok   bool
	}{
		{"pwd", []string{"pwd"}, true},
		{"git status", []string{"git", "status"}, true},
		{"/usr/bin/env FOO=1", nil, false}, // env assignment needs a shell
		{"echo $HOME", nil, false},         // substitution needs a shell
		{"cat *.go", nil, false},           // glob needs a shell
		{"a && b", nil, false},             // chain needs a shell
		{"a | b", nil, false},              // pipe needs a shell
		{"", nil, false},
	}
	for _, tc := range cases {
		argv, ok := directArgs(tc.cmd, "")
		if ok != tc.ok || !reflect.DeepEqual(argv, tc.argv) {
			t.Errorf("directArgs(%q) = %v, %v; want %v, %v",
				tc.cmd, argv, ok, tc.argv, tc.ok)
		}
	}
}

// TestDirectArgsWindows covers the Windows-specific word characters:
// a backslash is a path separator there, so native paths must stay on
// the direct-argv path instead of being routed through a shell.
func TestDirectArgsWindows(t *testing.T) {
	extra := shelldetect.Default("windows").SafeRunes
	cases := []struct {
		cmd  string
		argv []string
		ok   bool
	}{
		{`C:\tools\python.exe script.py`,
			[]string{`C:\tools\python.exe`, "script.py"}, true},
		{`py -3 C:\work\main.py`,
			[]string{"py", "-3", `C:\work\main.py`}, true},
		{"dir /b", []string{"dir", "/b"}, true},
		// Expansion and cmd escaping still need the shell.
		{"echo %PATH%", nil, false},
		{"echo foo ^& bar", nil, false},
		{"dir | findstr x", nil, false},
	}
	for _, tc := range cases {
		argv, ok := directArgs(tc.cmd, extra)
		if ok != tc.ok || !reflect.DeepEqual(argv, tc.argv) {
			t.Errorf("directArgs(%q) = %v, %v; want %v, %v",
				tc.cmd, argv, ok, tc.argv, tc.ok)
		}
	}
	// The same command stays ambiguous on POSIX, where a backslash is
	// an escape character: it must not take the direct path there.
	if _, ok := directArgs(`C:\tools\python.exe script.py`, ""); ok {
		t.Error("POSIX must not treat backslashes as literal word characters")
	}
}

// TestExecuteUsesConfiguredShell pins the shell wiring: the tool must
// spawn exactly the configured program and flags, whatever the host.
func TestExecuteUsesConfiguredShell(t *testing.T) {
	cases := []struct {
		name string
		spec shelldetect.Spec
		want []string
	}{
		{
			name: "posix",
			spec: shelldetect.Default("darwin"),
			want: []string{"/bin/sh", "-c", "rg --files | head"},
		},
		{
			name: "windows",
			spec: shelldetect.Default("windows"),
			want: []string{"cmd.exe", "/c", "rg --files | head"},
		},
		{
			name: "windows powershell",
			spec: shelldetect.Spec{
				Program:   `C:\tools\pwsh.exe`,
				Args:      []string{"-NoProfile", "-Command"},
				SafeRunes: `\`,
			},
			want: []string{
				`C:\tools\pwsh.exe`, "-NoProfile", "-Command",
				"rg --files | head",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &cmdRunner{
				out: sandbox.SessionOutput{
					NextSeq: 1,
					EOF:     true,
				},
				exit: sandbox.SessionExit{
					Code:   0,
					Reason: sandbox.SessionExited,
				},
			}
			tool, err := NewCommand(runner, WithShell(tc.spec))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tool.Execute(context.Background(),
				`{"command":"rg --files | head"}`); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runner.started[0].Argv, tc.want) {
				t.Fatalf("argv = %v, want %v", runner.started[0].Argv, tc.want)
			}
			if def := tool.Definition(); !strings.Contains(
				def.Description, tc.spec.CommandLine()) {
				t.Fatalf("description does not name the shell %q: %s",
					tc.spec.CommandLine(), def.Description)
			}
		})
	}
}

func TestExecuteRewritesPolicyDeniedWithRealCommand(t *testing.T) {
	tool, runner := newCommandTool()
	runner.err = errdefs.PolicyDeniedf(
		`sandbox: execution of "/bin/sh" denied: command not in sandbox allowlist`)
	_, err := tool.Execute(context.Background(),
		`{"command":"rg --files internal | rg httpclient"}`)
	if err == nil {
		t.Fatal("expected denial error")
	}
	if !errdefs.IsPolicyDenied(err) {
		t.Fatalf("error = %v, want PolicyDenied", err)
	}
	if !strings.Contains(err.Error(),
		`"rg --files internal | rg httpclient"`) {
		t.Errorf("error does not name the real command: %v", err)
	}
}

func TestExecuteTimeout(t *testing.T) {
	tool, runner := newCommandTool()
	if _, err := tool.Execute(context.Background(),
		`{"command":"sleep 1","timeout_seconds":2}`); err != nil {
		t.Fatal(err)
	}
	if runner.started[0].Opts.Timeout == 0 {
		t.Error("timeout not set")
	}
}

func TestExecuteRejectsEmptyCommand(t *testing.T) {
	tool, _ := newCommandTool()
	if _, err := tool.Execute(context.Background(), `{"command":""}`); err == nil {
		t.Fatal("empty command unexpectedly accepted")
	}
}

func TestCommandDefinition(t *testing.T) {
	tool, _ := newCommandTool()
	def := tool.Definition()
	if def.Name != CommandName || !strings.Contains(def.Description, "directly") {
		t.Fatalf("definition = %+v", def)
	}
	if !tool.Metadata().MutatesState {
		t.Fatal("exec_command must be mutating")
	}
}

// TestCommandAcceptsCmdAlias: several model harnesses name the command
// line "cmd". The alias costs nothing, while the schema keeps
// "command" as the documented name.
func TestCommandAcceptsCmdAlias(t *testing.T) {
	tool, runner := newCommandTool()
	if _, err := tool.Execute(context.Background(),
		`{"cmd":"rg --files","workdir":"internal"}`); err != nil {
		t.Fatalf("cmd alias: %v", err)
	}
	if len(runner.started) != 1 {
		t.Fatalf("started = %d sessions", len(runner.started))
	}
	if want := []string{"rg", "--files"}; !reflect.DeepEqual(
		runner.started[0].Argv, want) {
		t.Fatalf("argv = %v, want %v", runner.started[0].Argv, want)
	}
	if runner.started[0].Opts.WorkDir != "internal" {
		t.Fatalf("workdir = %q", runner.started[0].Opts.WorkDir)
	}
}

// TestCommandNamesUnknownArgument: the old loose decode dropped "cwd"
// and answered "command is required", which named neither the typo nor
// the accepted arguments.
func TestCommandNamesUnknownArgument(t *testing.T) {
	tool, _ := newCommandTool()
	_, err := tool.Execute(context.Background(),
		`{"cwd":"/tmp","command":"ls"}`)
	if err == nil {
		t.Fatal("unknown argument accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		`unknown argument "cwd"`,
		"accepted arguments:",
		"workdir",
		"command (alias: cmd)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}
