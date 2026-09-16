package exec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
)

// escalatingRunner answers differently depending on whether the call
// carries the approved-escalation marker, and reports whether the
// session may escalate at all.
type escalatingRunner struct {
	confinedCode int
	confinedText string
	hostCode     int
	hostText     string
	gate         bool
	escalated    []bool
}

func (r *escalatingRunner) Close() error { return nil }

func (r *escalatingRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{}
}

func (r *escalatingRunner) Start(
	ctx context.Context, _ sandbox.SessionSpec,
) (sandbox.Session, error) {
	escalated := ocsandbox.Escalating(ctx)
	r.escalated = append(r.escalated, escalated)
	code, text := r.confinedCode, r.confinedText
	if escalated {
		code, text = r.hostCode, r.hostText
	}
	stream := sandbox.SessionStreamStdout
	if code != 0 {
		stream = sandbox.SessionStreamStderr
	}
	if text == "" {
		text = "ok"
	}
	return &cmdSession{
		out: sandbox.SessionOutput{
			NextSeq: 1,
			Chunks: []sandbox.OutputChunk{{
				Seq: 0, Stream: stream, Data: []byte(text),
			}},
			EOF: true,
		},
		exit: sandbox.SessionExit{
			Code:   code,
			Reason: sandbox.SessionExited,
		},
	}, nil
}

func (r *escalatingRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}

func (r *escalatingRunner) Terminate(context.Context, string) error { return nil }

func (r *escalatingRunner) EscalationAvailable(context.Context) bool {
	return r.gate
}

type fakeEscalator struct {
	decision ocsandbox.EscalationDecision
	err      error
	requests []ocsandbox.EscalationRequest
}

func (f *fakeEscalator) Escalate(
	_ context.Context, req ocsandbox.EscalationRequest,
) (ocsandbox.EscalationDecision, error) {
	f.requests = append(f.requests, req)
	return f.decision, f.err
}

func escalationResult(t *testing.T, out string) map[string]any {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func newEscalationTool(
	t *testing.T,
	confinedExit int,
	escalator *fakeEscalator,
	gate bool,
) (*CommandTool, *escalatingRunner) {
	t.Helper()
	runner := &escalatingRunner{
		confinedCode: confinedExit,
		confinedText: "[Errno 1] Operation not permitted: '/Users/x/.local/lib'",
		hostCode:     0,
		hostText:     "installed",
		gate:         gate,
	}
	tool, err := NewCommand(
		runner, WithEscalator(escalator), WithShell(testPosixShell))
	if err != nil {
		t.Fatal(err)
	}
	return tool, runner
}

func TestEscalationRetriesOutsideSandbox(t *testing.T) {
	escalator := &fakeEscalator{
		decision: ocsandbox.EscalationDecision{Allow: true},
	}
	tool, runner := newEscalationTool(t, 1, escalator, true)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	res := escalationResult(t, out.Text())
	if res["exit_code"] != float64(0) {
		t.Fatalf("exit_code = %v, want the retried result", res["exit_code"])
	}
	note, _ := res["note"].(string)
	if !strings.Contains(note, "outside the sandbox") {
		t.Fatalf("note = %q", note)
	}
	if len(runner.escalated) != 2 || runner.escalated[0] || !runner.escalated[1] {
		t.Fatalf("escalation flags = %v, want [false true]", runner.escalated)
	}
	if len(escalator.requests) != 1 {
		t.Fatalf("escalation requests = %d", len(escalator.requests))
	}
	req := escalator.requests[0]
	if req.Command != "pip install requests" || req.Rule != "pip install requests" {
		t.Fatalf("request = %+v", req)
	}
	if !strings.Contains(req.Reason, "operation not permitted") {
		t.Fatalf("reason = %q", req.Reason)
	}
	if !strings.Contains(req.Detail, "Operation not permitted") {
		t.Fatalf("detail = %q", req.Detail)
	}
}

func TestEscalationRememberedNote(t *testing.T) {
	escalator := &fakeEscalator{
		decision: ocsandbox.EscalationDecision{Allow: true, Remember: true},
	}
	tool, _ := newEscalationTool(t, 1, escalator, true)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	note, _ := escalationResult(t, out.Text())["note"].(string)
	if !strings.Contains(note, "remembered") {
		t.Fatalf("note = %q", note)
	}
}

func TestEscalationDeniedKeepsConfinedResult(t *testing.T) {
	escalator := &fakeEscalator{}
	tool, runner := newEscalationTool(t, 1, escalator, true)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	res := escalationResult(t, out.Text())
	if res["exit_code"] != float64(1) {
		t.Fatalf("exit_code = %v, want the confined failure", res["exit_code"])
	}
	if _, ok := res["note"]; ok {
		t.Fatal("denied escalation must not add a note")
	}
	if len(runner.escalated) != 1 {
		t.Fatalf("calls = %v, want a single confined attempt", runner.escalated)
	}
}

func TestNoEscalationForOrdinaryFailure(t *testing.T) {
	escalator := &fakeEscalator{
		decision: ocsandbox.EscalationDecision{Allow: true},
	}
	tool, runner := newEscalationTool(t, 1, escalator, true)
	// Compile-style failure: not a sandbox refusal, so no prompt.
	runner.confinedText = "./main.go:12:2: undefined: foo"
	out, err := tool.Execute(context.Background(),
		`{"command":"go build ./..."}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("ordinary failure prompted: %+v", escalator.requests)
	}
	if len(runner.escalated) != 1 {
		t.Fatalf("calls = %v", runner.escalated)
	}
	if _, ok := escalationResult(t, out.Text())["note"]; ok {
		t.Fatal("ordinary failure must not carry a note")
	}
}

func TestNoEscalationWhenGateClosed(t *testing.T) {
	escalator := &fakeEscalator{
		decision: ocsandbox.EscalationDecision{Allow: true},
	}
	// Read-only / unconfined sessions report the gate as closed.
	tool, runner := newEscalationTool(t, 1, escalator, false)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("closed gate still prompted: %+v", escalator.requests)
	}
	if len(runner.escalated) != 1 {
		t.Fatalf("calls = %v", runner.escalated)
	}
	if _, ok := escalationResult(t, out.Text())["note"]; ok {
		t.Fatal("closed gate must not add a note")
	}
}

func TestEscalationWithoutEscalator(t *testing.T) {
	runner := &escalatingRunner{
		confinedCode: 1,
		confinedText: "Operation not permitted",
		hostCode:     0,
		gate:         true,
	}
	tool, err := NewCommand(runner)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res := escalationResult(t, out.Text()); res["exit_code"] != float64(1) {
		t.Fatalf("exit_code = %v", res["exit_code"])
	}
	if len(runner.escalated) != 1 {
		t.Fatalf("calls = %v", runner.escalated)
	}
}
