package execcommand

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/sdk/sandbox"
)

type recordingRunner struct {
	cmd  string
	args []string
	opts sandbox.ExecOptions
}

func (r *recordingRunner) Exec(
	_ context.Context,
	cmd string,
	args []string,
	opts sandbox.ExecOptions,
) (*sandbox.ExecResult, error) {
	r.cmd = cmd
	r.args = args
	r.opts = opts
	return &sandbox.ExecResult{ExitCode: 0, Stdout: "out", Stderr: ""}, nil
}

func TestExecuteRunsShellCommand(t *testing.T) {
	runner := &recordingRunner{}
	tool, err := New(runner)
	if err != nil {
		t.Fatal(err)
	}

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
	if runner.cmd != "/bin/sh" || len(runner.args) != 2 ||
		runner.args[0] != "-c" ||
		runner.args[1] != "rg --files internal | rg httpclient" {
		t.Errorf("argv = %q %v", runner.cmd, runner.args)
	}
}

func TestExecuteTimeout(t *testing.T) {
	runner := &recordingRunner{}
	tool, err := New(runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(),
		`{"command":"sleep 1","timeout_seconds":2}`); err != nil {
		t.Fatal(err)
	}
	if runner.opts.Timeout == 0 {
		t.Error("timeout not set")
	}
}

func TestExecuteRejectsEmptyCommand(t *testing.T) {
	tool, err := New(&recordingRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), `{"command":""}`); err == nil {
		t.Fatal("empty command unexpectedly accepted")
	}
}

func TestDefinition(t *testing.T) {
	tool, err := New(&recordingRunner{})
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "/bin/sh -c") {
		t.Fatalf("definition = %+v", def)
	}
	if !tool.Metadata().MutatesState {
		t.Fatal("exec_command must be mutating")
	}
}
