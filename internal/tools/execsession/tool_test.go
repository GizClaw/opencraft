package execsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

type fakeSession struct {
	output sandbox.SessionOutput
	exit   sandbox.SessionExit
}

func (s *fakeSession) ID() string { return "p" }
func (s *fakeSession) PID() int   { return 1 }
func (s *fakeSession) Capabilities() sandbox.SessionCapabilities {
	return sandbox.SessionCapabilities{TTY: true, Signal: true}
}
func (s *fakeSession) Read(
	context.Context, int64, int,
) (sandbox.SessionOutput, error) {
	return s.output, nil
}
func (s *fakeSession) Write(context.Context, []byte) error { return nil }
func (s *fakeSession) CloseInput() error                   { return nil }
func (s *fakeSession) Resize(context.Context, int, int) error {
	return nil
}
func (s *fakeSession) Signal(context.Context, sandbox.SessionSignal) error {
	return nil
}
func (s *fakeSession) Terminate(context.Context) error { return nil }
func (s *fakeSession) Wait(context.Context) (sandbox.SessionExit, error) {
	return s.exit, nil
}
func (s *fakeSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	return nil, nil
}
func (s *fakeSession) Close() error { return nil }

type fakeRunner struct {
	started sandbox.SessionSpec
	proc    *fakeSession
}

func (r *fakeRunner) Close() error { return nil }

func (r *fakeRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Features: sandbox.SessionFeatures{TTY: true, Signal: true},
	}
}
func (r *fakeRunner) Start(
	_ context.Context,
	spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	r.started = spec
	if r.proc == nil {
		r.proc = &fakeSession{}
	}
	return r.proc, nil
}
func (r *fakeRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}
func (r *fakeRunner) Terminate(context.Context, string) error { return nil }

func newTestTool() (*Tool, *fakeRunner) {
	runner := &fakeRunner{
		proc: &fakeSession{
			output: sandbox.SessionOutput{
				NextSeq: 1,
				Chunks: []sandbox.OutputChunk{{
					Seq:    0,
					Stream: sandbox.SessionStreamStdout,
					Data:   []byte("hi"),
				}},
				EOF: true,
			},
			exit: sandbox.SessionExit{Code: 0, Reason: sandbox.SessionExited},
		},
	}
	tool, err := New(runner)
	if err != nil {
		panic(err)
	}
	return tool, runner
}

func TestStartReadClose(t *testing.T) {
	tool, runner := newTestTool()
	ctx := context.Background()

	out, err := tool.Execute(ctx, `{"action":"start","process_id":"s1","argv":["/bin/sh"],"tty":true,"rows":24,"cols":80}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"started":true`) {
		t.Errorf("start = %s", out)
	}
	if runner.started.ID != "s1" || len(runner.started.Argv) != 1 ||
		!runner.started.TTY || runner.started.Rows != 24 || runner.started.Cols != 80 {
		t.Errorf("spec = %+v", runner.started)
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
	tool, _ := newTestTool()
	if _, err := tool.Execute(context.Background(),
		`{"action":"read","process_id":"nope"}`); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestDefinition(t *testing.T) {
	tool, _ := newTestTool()
	def := tool.Definition()
	if def.Name != Name {
		t.Fatalf("definition = %+v", def)
	}
}
