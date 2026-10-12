package exec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
)

// The command tool does not own escalation: the runner serves the front
// end (sandbox.EscalationProvider) and the tool reports what it did.
// These tests pin the tool's half — which results get a note, and that
// nothing prompts when the runner serves no front end.

// escalationProvider is the runner shape the wiring produces: a
// confined chain that answers the provider question with the front end
// the tool should use.
type escalationProvider struct {
	sandbox.Runner
	front *sandbox.Escalation
}

func (p escalationProvider) Escalation() *sandbox.Escalation { return p.front }

type fakeEscalator struct {
	decision sandbox.EscalationDecision
	err      error
	requests []sandbox.EscalationRequest
}

func (f *fakeEscalator) Escalate(
	_ context.Context, req sandbox.EscalationRequest,
) (sandbox.EscalationDecision, error) {
	f.requests = append(f.requests, req)
	return f.decision, f.err
}

// stubRuleStore matches the rules the test registered.
type stubRuleStore struct {
	rules []string
}

func (s stubRuleStore) EscalatedAllowed(req sandbox.ExecRequest) bool {
	got := sandbox.RuleFor(append([]string{req.Command}, req.Args...))
	for _, rule := range s.rules {
		if rule == got {
			return true
		}
	}
	return false
}

// runOutput builds the finished-session output for one exit code, on
// stderr when the command failed.
func runOutput(text string, code int) (sandbox.SessionOutput, sandbox.SessionExit) {
	stream := sandbox.SessionStreamStdout
	if code != 0 {
		stream = sandbox.SessionStreamStderr
	}
	return sandbox.SessionOutput{
		NextSeq: 1,
		Chunks: []sandbox.OutputChunk{{
			Seq: 0, Stream: stream, Data: []byte(text),
		}},
		EOF: true,
	}, sandbox.SessionExit{Code: code, Reason: sandbox.SessionExited}
}

// newEscalationTool wires the command tool the way the deployment does:
// a confined chain that refuses, a host chain that succeeds, and the
// gate the session mode owns.
func newEscalationTool(
	t *testing.T,
	escalator *fakeEscalator,
	gate bool,
	rules sandbox.EscalationRules,
) (*CommandTool, *cmdRunner, *cmdRunner) {
	t.Helper()
	confinedOut, confinedExit := runOutput(
		"[Errno 1] Operation not permitted: '/Users/x/.local/lib'", 1)
	confined := &cmdRunner{out: confinedOut, exit: confinedExit}
	hostOut, hostExit := runOutput("installed", 0)
	host := &cmdRunner{out: hostOut, exit: hostExit}
	front := &sandbox.Escalation{
		Confined:   confined,
		Unconfined: host,
		Escalate:   escalator,
		Rules:      rules,
		Gate: sandbox.EscalationGateFunc(
			func(context.Context) bool { return gate }),
	}
	tool, err := NewCommand(
		escalationProvider{Runner: confined, front: front},
		WithShell(testPosixShell))
	if err != nil {
		t.Fatal(err)
	}
	return tool, confined, host
}

func escalationResult(t *testing.T, out string) map[string]any {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEscalationRetriesOutsideSandbox(t *testing.T) {
	escalator := &fakeEscalator{
		decision: sandbox.EscalationDecision{Allow: true},
	}
	tool, confined, host := newEscalationTool(t, escalator, true, nil)
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
	if len(confined.started) != 1 || len(host.started) != 1 {
		t.Fatalf("starts: confined=%d host=%d",
			len(confined.started), len(host.started))
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
		decision: sandbox.EscalationDecision{Allow: true, Remember: true},
	}
	tool, _, _ := newEscalationTool(t, escalator, true, nil)
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

// TestEscalationRuleSkipsConfinedAttempt pins the note for the path
// where nothing was refused: a remembered rule ran the command on the
// host from the start.
func TestEscalationRuleSkipsConfinedAttempt(t *testing.T) {
	escalator := &fakeEscalator{
		decision: sandbox.EscalationDecision{Allow: true},
	}
	rules := stubRuleStore{rules: []string{"pip install requests"}}
	tool, confined, host := newEscalationTool(t, escalator, true, rules)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	res := escalationResult(t, out.Text())
	if res["exit_code"] != float64(0) {
		t.Fatalf("exit_code = %v", res["exit_code"])
	}
	note, _ := res["note"].(string)
	if !strings.Contains(note, "remembered") {
		t.Fatalf("note = %q", note)
	}
	if len(confined.started) != 0 || len(host.started) != 1 ||
		len(escalator.requests) != 0 {
		t.Fatalf("remembered rule: confined=%d host=%d prompts=%d",
			len(confined.started), len(host.started), len(escalator.requests))
	}
}

func TestEscalationDeniedKeepsConfinedResult(t *testing.T) {
	escalator := &fakeEscalator{}
	tool, confined, host := newEscalationTool(t, escalator, true, nil)
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
	if len(confined.started) != 1 || len(host.started) != 0 {
		t.Fatalf("calls: confined=%d host=%d",
			len(confined.started), len(host.started))
	}
}

func TestNoEscalationForOrdinaryFailure(t *testing.T) {
	escalator := &fakeEscalator{
		decision: sandbox.EscalationDecision{Allow: true},
	}
	tool, confined, host := newEscalationTool(t, escalator, true, nil)
	// Compile-style failure: not a sandbox refusal, so no prompt.
	out, exit := runOutput("./main.go:12:2: undefined: foo", 1)
	confined.out, confined.exit = out, exit
	out2, err := tool.Execute(context.Background(),
		`{"command":"go build ./..."}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("ordinary failure prompted: %+v", escalator.requests)
	}
	if len(confined.started) != 1 || len(host.started) != 0 {
		t.Fatalf("calls: confined=%d host=%d",
			len(confined.started), len(host.started))
	}
	if _, ok := escalationResult(t, out2.Text())["note"]; ok {
		t.Fatal("ordinary failure must not carry a note")
	}
}

func TestNoEscalationWhenGateClosed(t *testing.T) {
	escalator := &fakeEscalator{
		decision: sandbox.EscalationDecision{Allow: true},
	}
	// Read-only / unconfined sessions report the gate as closed.
	tool, confined, host := newEscalationTool(t, escalator, false, nil)
	out, err := tool.Execute(context.Background(),
		`{"command":"pip install requests"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("closed gate still prompted: %+v", escalator.requests)
	}
	if len(confined.started) != 1 || len(host.started) != 0 {
		t.Fatalf("calls: confined=%d host=%d",
			len(confined.started), len(host.started))
	}
	if _, ok := escalationResult(t, out.Text())["note"]; ok {
		t.Fatal("closed gate must not add a note")
	}
}

// TestEscalationWithoutFrontEnd pins the headless and embedded shape: a
// runner that serves no front end (no escalator wired, or nothing wired
// at all) leaves a refusal as a plain command failure.
func TestEscalationWithoutFrontEnd(t *testing.T) {
	out, exit := runOutput("Operation not permitted", 1)
	for name, runner := range map[string]sandbox.Runner{
		"no provider": &cmdRunner{out: out, exit: exit},
		"nil front": escalationProvider{
			Runner: &cmdRunner{out: out, exit: exit},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tool, err := NewCommand(runner, WithShell(testPosixShell))
			if err != nil {
				t.Fatal(err)
			}
			res, err := tool.Execute(context.Background(),
				`{"command":"pip install requests"}`)
			if err != nil {
				t.Fatal(err)
			}
			body := escalationResult(t, res.Text())
			if body["exit_code"] != float64(1) {
				t.Fatalf("exit_code = %v", body["exit_code"])
			}
			if _, ok := body["note"]; ok {
				t.Fatal("no front end must not add a note")
			}
		})
	}
}

// The provider interface is how the tool finds the front end; keep the
// assertion honest if the wiring changes shape.
var _ ocsandbox.EscalationProvider = escalationProvider{}
