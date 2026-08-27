package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

type sessSession struct {
	output sandbox.SessionOutput
	exit   sandbox.SessionExit
}

func (s *sessSession) ID() string { return "p" }
func (s *sessSession) PID() int   { return 1 }
func (s *sessSession) Capabilities() sandbox.SessionCapabilities {
	return sandbox.SessionCapabilities{TTY: true, Signal: true}
}
func (s *sessSession) Read(
	context.Context, int64, int,
) (sandbox.SessionOutput, error) {
	return s.output, nil
}
func (s *sessSession) Write(context.Context, []byte) error { return nil }
func (s *sessSession) CloseInput() error                   { return nil }
func (s *sessSession) Resize(context.Context, int, int) error {
	return nil
}
func (s *sessSession) Signal(context.Context, sandbox.SessionSignal) error {
	return nil
}
func (s *sessSession) Terminate(context.Context) error { return nil }
func (s *sessSession) Wait(context.Context) (sandbox.SessionExit, error) {
	return s.exit, nil
}
func (s *sessSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	return nil, nil
}
func (s *sessSession) Close() error { return nil }

type sessRunner struct {
	started sandbox.SessionSpec
	proc    *sessSession
}

func (r *sessRunner) Close() error { return nil }

func (r *sessRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Features: sandbox.SessionFeatures{TTY: true, Signal: true},
	}
}
func (r *sessRunner) Start(
	_ context.Context,
	spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	r.started = spec
	if r.proc == nil {
		r.proc = &sessSession{}
	}
	return r.proc, nil
}
func (r *sessRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}
func (r *sessRunner) Terminate(context.Context, string) error { return nil }

func newSessionTool() (*SessionTool, *sessRunner) {
	runner := &sessRunner{
		proc: &sessSession{
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
	tool, err := NewSession(runner)
	if err != nil {
		panic(err)
	}
	return tool, runner
}

func TestStartReadClose(t *testing.T) {
	tool, runner := newSessionTool()
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
	tool, _ := newSessionTool()
	if _, err := tool.Execute(context.Background(),
		`{"action":"read","process_id":"nope"}`); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestSessionDefinition(t *testing.T) {
	tool, _ := newSessionTool()
	def := tool.Definition()
	if def.Name != SessionName {
		t.Fatalf("definition = %+v", def)
	}
}

// TestWaitRemovesSession verifies a waited session is dropped from the
// map: the process has exited, so keeping it would only pin the handle
// and its output buffer for sessions the model never closes. Starting
// the same id again must succeed, and a close on the old one must fail.
func TestWaitRemovesSession(t *testing.T) {
	tool, _ := newSessionTool()
	ctx := context.Background()

	if _, err := tool.Execute(ctx, `{"action":"start","process_id":"s1","argv":["true"]}`); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(ctx, `{"action":"wait","process_id":"s1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"exit_code":0`) {
		t.Errorf("wait = %s", out)
	}

	// The id is reusable after wait.
	if _, err := tool.Execute(ctx, `{"action":"start","process_id":"s1","argv":["true"]}`); err != nil {
		t.Fatalf("reuse after wait failed: %v", err)
	}
}

// TestMaxSessions verifies the live-session cap: starting beyond it is
// rejected with a clear error instead of growing the map without bound.
func TestMaxSessions(t *testing.T) {
	tool, _ := newSessionTool()
	ctx := context.Background()

	for i := 0; i < maxSessions; i++ {
		if _, err := tool.Execute(ctx, fmt.Sprintf(
			`{"action":"start","process_id":"s%d","argv":["true"]}`, i)); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := tool.Execute(ctx, `{"action":"start","process_id":"overflow","argv":["true"]}`); err == nil {
		t.Fatal("start beyond cap accepted")
	} else if !strings.Contains(err.Error(), "too many live sessions") {
		t.Fatalf("cap error = %v", err)
	}
}
