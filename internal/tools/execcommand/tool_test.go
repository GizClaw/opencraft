package execcommand

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/execd"
)

type fakeEnv struct {
	req    execd.Request
	result execd.Result
}

func (f *fakeEnv) ID() string { return "fake" }

func (f *fakeEnv) Capabilities() []execd.Capability {
	return []execd.Capability{execd.CapExec}
}

func (f *fakeEnv) Exec(
	_ context.Context,
	req execd.Request,
) (execd.Result, error) {
	f.req = req
	return f.result, nil
}

func (f *fakeEnv) Start(context.Context, execd.Spec) (execd.Process, error) {
	return nil, nil
}

func TestExecuteRunsShellCommand(t *testing.T) {
	env := &fakeEnv{result: execd.Result{ExitCode: 0, Stdout: "out"}}
	tool, err := New(env)
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
	want := []string{"/bin/sh", "-c", "rg --files internal | rg httpclient"}
	if !reflect.DeepEqual(env.req.Argv, want) {
		t.Errorf("argv = %v", env.req.Argv)
	}
}

func TestExecuteTimeout(t *testing.T) {
	env := &fakeEnv{}
	tool, err := New(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(),
		`{"command":"sleep 1","timeout_seconds":2}`); err != nil {
		t.Fatal(err)
	}
	if env.req.Timeout == 0 {
		t.Error("timeout not set")
	}
}

func TestExecuteRejectsEmptyCommand(t *testing.T) {
	tool, err := New(&fakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), `{"command":""}`); err == nil {
		t.Fatal("empty command unexpectedly accepted")
	}
}

func TestDefinition(t *testing.T) {
	tool, err := New(&fakeEnv{})
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
