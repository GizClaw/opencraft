package exec

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

// gateRunner holds every start until release is closed, which is how the
// tests make the launch window — the stretch a real start spends forking
// a process — wide enough to race against.
type gateRunner struct {
	release chan struct{}
	starts  chan sandbox.SessionSpec
	proc    *sessSession

	mu      sync.Mutex
	started int
}

func (r *gateRunner) Close() error { return nil }

func (r *gateRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Features: sandbox.SessionFeatures{TTY: true, Signal: true},
	}
}

func (r *gateRunner) Start(
	ctx context.Context, spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	r.mu.Lock()
	r.started++
	r.mu.Unlock()
	select {
	case r.starts <- spec:
	default:
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.proc == nil {
		r.proc = &sessSession{}
	}
	return r.proc, nil
}

func (r *gateRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}

func (r *gateRunner) Terminate(context.Context, string) error { return nil }

func (r *gateRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

func newGatedTool() (*SessionTool, *gateRunner) {
	runner := &gateRunner{
		release: make(chan struct{}),
		starts:  make(chan sandbox.SessionSpec, 128),
		proc: &sessSession{
			exit: sandbox.SessionExit{Code: 0, Reason: sandbox.SessionExited},
		},
	}
	tool, err := NewSession(runner)
	if err != nil {
		panic(err)
	}
	return tool, runner
}

// One id is one session, including while the launch is in flight. The
// slot is claimed before the runner is asked to start, so a second
// concurrent start of the same id is refused instead of launching a
// process nothing will ever point at.
func TestStartClaimsTheIDBeforeLaunching(t *testing.T) {
	tool, runner := newGatedTool()
	ctx := context.Background()

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tool.Execute(ctx,
				`{"action":"start","process_id":"race","argv":["true"]}`)
			results <- err
		}()
	}
	// Wait until the winner is inside the runner before letting the
	// second caller in.
	<-runner.starts
	close(runner.release)
	wg.Wait()
	close(results)

	var accepted, refused int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case strings.Contains(err.Error(), "already exists"):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("accepted=%d refused=%d, want exactly one of each",
			accepted, refused)
	}
	if got := runner.startCount(); got != 1 {
		t.Fatalf("runner started %d processes, want 1", got)
	}
}

// The session cap counts claims too: a launch in flight occupies its
// slot, so concurrent starts cannot all pass a check that only sees
// registered sessions.
func TestStartCapCountsInFlightLaunches(t *testing.T) {
	tool, runner := newGatedTool()
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, maxSessions+4)
	for i := 0; i < len(errs); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = tool.Execute(ctx,
				`{"action":"start","process_id":"`+string(rune('a'+i%26))+string(rune('0'+i/26))+`","argv":["true"]}`)
		}(i)
	}
	// Let every caller reach the runner, then release them all.
	for i := 0; i < maxSessions; i++ {
		<-runner.starts
	}
	close(runner.release)
	wg.Wait()

	accepted := 0
	for _, err := range errs {
		if err == nil {
			accepted++
			continue
		}
		if !strings.Contains(err.Error(), "too many live sessions") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted > maxSessions {
		t.Fatalf("accepted %d sessions, want at most %d", accepted, maxSessions)
	}
	if runner.startCount() != accepted {
		t.Fatalf("runner started %d processes for %d accepted sessions",
			runner.startCount(), accepted)
	}
}

// A wait that returns after its id was started again must not take the
// new session's slot with it: the map is keyed by id, and the slot it
// removes is identified by its epoch, not by the name it was asked
// about.
func TestWaitDoesNotEvictAReusedID(t *testing.T) {
	ctx := context.Background()

	first := &gatedSession{
		Session: &sessSession{exit: sandbox.SessionExit{
			Code: 3, Reason: sandbox.SessionExited}},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	second := &sessSession{exit: sandbox.SessionExit{
		Code: 0, Reason: sandbox.SessionExited}}

	// A runner that hands out the first session's Wait a gate of its
	// own, so the test decides when the old wait returns.
	runner := &scriptedRunner{sessions: []sandbox.Session{
		first, second}}
	tool2, err := NewSession(runner)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tool2.Execute(ctx, `{"action":"start","process_id":"reuse","argv":["true"]}`); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() {
		_, err := tool2.Execute(ctx, `{"action":"wait","process_id":"reuse"}`)
		waitDone <- err
	}()
	// The old wait is inside the session, blocked, before the id is
	// reused.
	<-first.entered
	// The session has been replaced by the time the old wait returns.
	if _, err := tool2.Execute(ctx, `{"action":"close","process_id":"reuse"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool2.Execute(ctx, `{"action":"start","process_id":"reuse","argv":["true"]}`); err != nil {
		t.Fatal(err)
	}
	close(first.release)
	if err := <-waitDone; err != nil {
		t.Fatalf("wait after reuse: %v", err)
	}
	if _, err := tool2.Execute(ctx, `{"action":"read","process_id":"reuse"}`); err != nil {
		t.Fatalf("the replaced session was evicted by the old wait: %v", err)
	}
}

// scriptedRunner hands out its sessions in order.
type scriptedRunner struct {
	mu       sync.Mutex
	sessions []sandbox.Session
	next     int
}

func (r *scriptedRunner) Close() error { return nil }

func (r *scriptedRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Features: sandbox.SessionFeatures{TTY: true, Signal: true},
	}
}

func (r *scriptedRunner) Start(
	context.Context, sandbox.SessionSpec,
) (sandbox.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.next >= len(r.sessions) {
		return &sessSession{}, nil
	}
	proc := r.sessions[r.next]
	r.next++
	return proc, nil
}

func (r *scriptedRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}

func (r *scriptedRunner) Terminate(context.Context, string) error { return nil }

// gatedSession reports when Wait is entered and blocks there until
// release is closed.
type gatedSession struct {
	sandbox.Session
	entered chan struct{}
	release chan struct{}
}

func (s *gatedSession) Wait(ctx context.Context) (sandbox.SessionExit, error) {
	close(s.entered)
	<-s.release
	return s.Session.Wait(ctx)
}
