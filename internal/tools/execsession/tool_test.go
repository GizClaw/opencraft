package execsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/execd"
)

type fakeProcess struct {
	output []execd.Chunk
	eof    bool
	exit   execd.Exit
}

func (p *fakeProcess) ID() string { return "p" }
func (p *fakeProcess) Read(context.Context, int64, int) (execd.Output, error) {
	return execd.Output{NextSeq: 1, Chunks: p.output, EOF: p.eof}, nil
}
func (p *fakeProcess) Write(context.Context, []byte) error { return nil }
func (p *fakeProcess) Terminate(context.Context) error     { return nil }
func (p *fakeProcess) Wait(context.Context) (execd.Exit, error) {
	return p.exit, nil
}
func (p *fakeProcess) Close() error { return nil }
func (p *fakeProcess) Signal(context.Context, execd.Signal) error {
	return nil
}
func (p *fakeProcess) Resize(context.Context, int, int) error { return nil }

type fakeEnv struct {
	started execd.Spec
	proc    *fakeProcess
}

func (e *fakeEnv) ID() string { return "fake" }
func (e *fakeEnv) Capabilities() []execd.Capability {
	return []execd.Capability{
		execd.CapSession, execd.CapSignal, execd.CapPTY,
	}
}
func (e *fakeEnv) Exec(context.Context, execd.Request) (execd.Result, error) {
	return execd.Result{}, nil
}
func (e *fakeEnv) Start(_ context.Context, spec execd.Spec) (execd.Process, error) {
	e.started = spec
	if e.proc == nil {
		e.proc = &fakeProcess{}
	}
	return e.proc, nil
}

func TestStartReadClose(t *testing.T) {
	env := &fakeEnv{proc: &fakeProcess{
		output: []execd.Chunk{{Seq: 0, Stream: execd.Stdout, Data: []byte("hi")}},
		eof:    true,
	}}
	tool, err := New(env)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	out, err := tool.Execute(ctx, `{"action":"start","process_id":"s1","argv":["/bin/sh"],"tty":true,"rows":24,"cols":80}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"started":true`) {
		t.Errorf("start = %s", out)
	}
	if env.started.ID != "s1" || len(env.started.Argv) != 1 || !env.started.TTY {
		t.Errorf("spec = %+v", env.started)
	}

	out, err = tool.Execute(ctx, `{"action":"read","process_id":"s1","after_seq":0,"max_bytes":4096}`)
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		Chunks []map[string]any `json:"chunks"`
		EOF    bool             `json:"eof"`
	}
	if err := json.Unmarshal([]byte(out), &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Chunks) != 1 || read.Chunks[0]["data"] != "hi" || !read.EOF {
		t.Errorf("read = %s", out)
	}

	out, err = tool.Execute(ctx, `{"action":"close","process_id":"s1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"closed":true`) {
		t.Errorf("close = %s", out)
	}
	if _, err := tool.Execute(ctx, `{"action":"read","process_id":"s1"}`); err == nil {
		t.Fatal("read on closed session should fail")
	}
}

func TestUnknownSession(t *testing.T) {
	tool, err := New(&fakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(),
		`{"action":"read","process_id":"nope"}`); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestDefinition(t *testing.T) {
	tool, err := New(&fakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "start") {
		t.Fatalf("definition = %+v", def)
	}
	if !tool.Metadata().MutatesState {
		t.Fatal("exec_session must be mutating")
	}
}
