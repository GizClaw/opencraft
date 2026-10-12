package sandbox

import (
	"context"
	"strings"
	"testing"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// The refusal detection, the prompt and the retry itself live in
// flowcraft core (core/sandbox/escalation.go and its tests). What these
// tests pin is this repository's half: which chains the front end is
// wired to, and which sessions are offered one at all.

// scriptedRunner answers every Start with an already finished session,
// so coresandbox.Exec sees a complete result. It plays the confined
// chain (refusal output) or the unconfined retry target (success)
// depending on how the test wires it.
type scriptedRunner struct {
	code   int
	text   string
	starts int
	opts   []coresandbox.ExecOptions
}

func (r *scriptedRunner) Close() error { return nil }

func (r *scriptedRunner) Capabilities() coresandbox.Capabilities {
	return coresandbox.Capabilities{}
}

func (r *scriptedRunner) Start(
	_ context.Context, spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	r.starts++
	r.opts = append(r.opts, spec.Opts)
	stream := coresandbox.SessionStreamStdout
	if r.code != 0 {
		stream = coresandbox.SessionStreamStderr
	}
	return &finishedSession{
		out: coresandbox.SessionOutput{
			NextSeq: 1,
			Chunks: []coresandbox.OutputChunk{{
				Seq: 0, Stream: stream, Data: []byte(r.text),
			}},
			EOF: true,
		},
		exit: coresandbox.SessionExit{
			Code: r.code, Reason: coresandbox.SessionExited,
		},
	}, nil
}

func (r *scriptedRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	return nil, nil
}

func (r *scriptedRunner) Terminate(context.Context, string) error { return nil }

// finishedSession is over the moment it is started.
type finishedSession struct {
	out  coresandbox.SessionOutput
	exit coresandbox.SessionExit
}

func (s *finishedSession) ID() string { return "s" }
func (s *finishedSession) PID() int   { return 1 }
func (s *finishedSession) Capabilities() coresandbox.SessionCapabilities {
	return coresandbox.SessionCapabilities{}
}
func (s *finishedSession) Read(
	context.Context, int64, int,
) (coresandbox.SessionOutput, error) {
	return s.out, nil
}
func (s *finishedSession) Write(context.Context, []byte) error { return nil }
func (s *finishedSession) CloseInput() error                   { return nil }
func (s *finishedSession) Resize(context.Context, int, int) error {
	return nil
}
func (s *finishedSession) Signal(context.Context, coresandbox.SessionSignal) error {
	return nil
}
func (s *finishedSession) Terminate(context.Context) error { return nil }
func (s *finishedSession) Wait(context.Context) (coresandbox.SessionExit, error) {
	return s.exit, nil
}
func (s *finishedSession) Watch(context.Context) (coresandbox.SessionWatcher, error) {
	return nil, nil
}
func (s *finishedSession) Close() error { return nil }

// stubRules is the read side of the persisted escalation rules: it
// matches whatever the test registered, normalized the way the store
// does.
type stubRules struct {
	rules []string
}

func (s stubRules) EscalatedAllowed(req coresandbox.ExecRequest) bool {
	got := coresandbox.RuleFor(append([]string{req.Command}, req.Args...))
	for _, rule := range s.rules {
		if rule == got {
			return true
		}
	}
	return false
}

// stubEscalator answers with one fixed decision and records the ask.
type stubEscalator struct {
	decision coresandbox.EscalationDecision
	requests []coresandbox.EscalationRequest
}

func (s *stubEscalator) Escalate(
	_ context.Context, req coresandbox.EscalationRequest,
) (coresandbox.EscalationDecision, error) {
	s.requests = append(s.requests, req)
	return s.decision, nil
}

const refusalText = "[Errno 1] Operation not permitted: '/Users/x/.local/lib'"

// newEscalationHost wires a HostSandbox the way the resource factory
// does — this runner as the confined chain, host as the retry target —
// and returns the front end the exec tool would pick up.
func newEscalationHost(
	t *testing.T,
	mode sessions.Mode,
	escalator coresandbox.Escalator,
	rules coresandbox.EscalationRules,
) (*HostSandbox, *scriptedRunner, *scriptedRunner, *sessions.Store, string) {
	t.Helper()
	skipIfYoloOnly(t)
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if mode != "" {
		if err := store.SetMode(context.Background(), id, mode); err != nil {
			t.Fatal(err)
		}
	}
	confined := &scriptedRunner{code: 1, text: refusalText}
	host := &scriptedRunner{text: "installed"}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: host,
		escalator:  escalator,
		rules:      rules,
	}
	return hs, confined, host, store, id
}

func TestEscalationFrontEndRetriesOnHost(t *testing.T) {
	escalator := &stubEscalator{
		decision: coresandbox.EscalationDecision{Allow: true},
	}
	hs, confined, host, _, id := newEscalationHost(t, "", escalator, nil)

	res, outcome, err := hs.Escalation().Exec(
		sessionCtx(id), "pip", []string{"install", "requests"},
		coresandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if confined.starts != 1 || host.starts != 1 {
		t.Fatalf("starts: confined=%d host=%d", confined.starts, host.starts)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "installed") {
		t.Fatalf("result = %+v, want the retry's", res)
	}
	if !outcome.Refused || !outcome.Approved || outcome.Remembered {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(escalator.requests) != 1 {
		t.Fatalf("prompts = %d", len(escalator.requests))
	}
	req := escalator.requests[0]
	if req.Rule != "pip install requests" {
		t.Fatalf("rule = %q", req.Rule)
	}
	if !strings.Contains(req.Reason, "operation not permitted") {
		t.Fatalf("reason = %q", req.Reason)
	}
	if !strings.Contains(req.Detail, "Operation not permitted") {
		t.Fatalf("detail = %q", req.Detail)
	}
}

func TestEscalationFrontEndDeniedKeepsConfinedResult(t *testing.T) {
	escalator := &stubEscalator{}
	hs, confined, host, _, id := newEscalationHost(t, "", escalator, nil)

	res, outcome, err := hs.Escalation().Exec(
		sessionCtx(id), "pip", []string{"install", "requests"},
		coresandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 1 || host.starts != 0 || confined.starts != 1 {
		t.Fatalf("confined=%d host=%d result=%+v",
			confined.starts, host.starts, res)
	}
	if !outcome.Refused || outcome.Approved {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestEscalationFrontEndRememberedRuleSkipsConfine(t *testing.T) {
	escalator := &stubEscalator{decision: coresandbox.EscalationDecision{
		Allow: true, Remember: true,
	}}
	rules := stubRules{rules: []string{"pip install requests"}}
	hs, confined, host, _, id := newEscalationHost(t, "", escalator, rules)
	front := hs.Escalation()

	// The rule already covers this command, so the confined attempt is
	// skipped from the start and the user is never asked again.
	res, outcome, err := front.Exec(sessionCtx(id), "pip",
		[]string{"install", "requests"}, coresandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if confined.starts != 0 {
		t.Fatalf("confined attempts = %d, want a remembered rule to skip them",
			confined.starts)
	}
	if host.starts != 1 {
		t.Fatalf("host runs = %d, want the covered call on the host", host.starts)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("prompts = %d, want none", len(escalator.requests))
	}
	if res.ExitCode != 0 || !outcome.Approved || !outcome.Remembered ||
		outcome.Refused {
		t.Fatalf("outcome = %+v result=%+v", outcome, res)
	}
}

func TestEscalationFrontEndReadOnlyKeepsConfine(t *testing.T) {
	escalator := &stubEscalator{
		decision: coresandbox.EscalationDecision{Allow: true},
	}
	rules := stubRules{rules: []string{"pip install requests"}}
	hs, confined, host, _, id := newEscalationHost(
		t, sessions.ModeReadOnly, escalator, rules)

	res, outcome, err := hs.Escalation().Exec(
		sessionCtx(id), "pip", []string{"install", "requests"},
		coresandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 1 || confined.starts != 1 || host.starts != 0 {
		t.Fatalf("read-only escaped: confined=%d host=%d result=%+v",
			confined.starts, host.starts, res)
	}
	if len(escalator.requests) != 0 {
		t.Fatalf("read-only session was prompted: %+v", escalator.requests)
	}
	// The confined attempt was refused — that is what read-only mode
	// does to a command that needs to write — but nothing widened the
	// boundary for it.
	if outcome.Approved || outcome.Remembered {
		t.Fatalf("outcome = %+v", outcome)
	}
	if confined.opts[0].Write != coresandbox.WriteReadOnly {
		t.Fatalf("opts = %+v", confined.opts[0])
	}
}

// TestHostSandboxSessionIgnoresRules pins the blast radius of a
// remembered rule: the front end is command-shaped, so only the exec
// command tool ever consults a rule. A session started straight on the
// runner (exec_session, a tool spawning its own process) runs under its
// session's mode.
func TestHostSandboxSessionIgnoresRules(t *testing.T) {
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
		rules:      stubRules{rules: []string{"pip install requests"}},
	}
	if _, err := hs.Start(sessionCtx(id), coresandbox.SessionSpec{
		ID:   "s",
		Argv: []string{"/bin/sh", "-c", "pip install requests"},
	}); err != nil {
		t.Fatal(err)
	}
	if unconfined.count() != 0 || len(confined.got) != 1 {
		t.Fatalf("a rule reached a session spawn: unconfined=%d confined=%d",
			unconfined.count(), len(confined.got))
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
		context.Background(), id, sessions.ModeReadOnly,
	); err != nil {
		t.Fatal(err)
	}
	if hs.EscalationAvailable(sessionCtx(id)) {
		t.Fatal("read-only sessions must not be asked to leave the confine")
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

// TestHostSandboxEscalationNeedsEscalator pins the provider contract the
// exec tool keys off: without an escalator or a rule store the runner
// serves no front end, and a refusal stays an ordinary failure.
func TestHostSandboxEscalationNeedsEscalator(t *testing.T) {
	hs := &HostSandbox{sessions: newTestStore(t)}
	if got := hs.Escalation(); got != nil {
		t.Fatalf("Escalation = %+v, want nil without a wired escalator", got)
	}
	var nilHost *HostSandbox
	if got := nilHost.Escalation(); got != nil {
		t.Fatalf("nil receiver Escalation = %+v", got)
	}
	hs.escalator = &stubEscalator{}
	hs.unconfined = &scriptedRunner{}
	front := hs.Escalation()
	if front == nil {
		t.Fatal("wired escalator must yield a front end")
	}
	if front.Confined == nil || front.Unconfined == nil || front.Gate == nil {
		t.Fatalf("front end = %+v", front)
	}
}

// TestEscalatingBridgeMarksStarts pins the remote half: the execd child
// picks its own unconfined runner from the marker, and the bridge must
// not close the backend it shares with the confined chain.
func TestEscalatingBridgeMarksStarts(t *testing.T) {
	inner := &markerRunner{}
	bridge := escalatingBridge{inner}
	if _, err := bridge.Start(
		context.Background(), coresandbox.SessionSpec{ID: "x"},
	); err != nil {
		t.Fatal(err)
	}
	if !inner.marked {
		t.Fatal("bridge did not mark the forwarded start")
	}
	if bridge.Close() != nil {
		t.Fatal("bridge must not close the shared backend itself")
	}
	if escalated(context.Background()) {
		t.Fatal("a plain context reads as escalated")
	}
}

type markerRunner struct {
	marked bool
}

func (r *markerRunner) Close() error { return nil }

func (r *markerRunner) Capabilities() coresandbox.Capabilities {
	return coresandbox.Capabilities{}
}

func (r *markerRunner) Start(
	ctx context.Context, _ coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	r.marked = escalated(ctx)
	return nil, nil
}

func (r *markerRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	return nil, nil
}

func (r *markerRunner) Terminate(context.Context, string) error { return nil }
