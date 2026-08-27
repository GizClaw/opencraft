package exec

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"
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
	tool, err := NewCommand(runner)
	if err != nil {
		panic(err)
	}
	return tool, runner
}

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
	if err := json.Unmarshal([]byte(out), &res); err != nil {
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
		argv, ok := directArgs(tc.cmd)
		if ok != tc.ok || !reflect.DeepEqual(argv, tc.argv) {
			t.Errorf("directArgs(%q) = %v, %v; want %v, %v",
				tc.cmd, argv, ok, tc.argv, tc.ok)
		}
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
