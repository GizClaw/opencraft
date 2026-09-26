// Host is the public handle one workspace's runtime is reached
// through: run bookkeeping, close/shutdown handshake and the accessors
// adapters use.

package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/telemetry"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Host is one shared workspace runtime.
type Host struct {
	workDir     string
	userDir     string
	workspaceID string
	store       *sessions.Store
	ctrl        *engine.Controller
	broker      *interact.Broker
	manager     *Manager
	agents      atomic.Pointer[ocsagents.Lifecycle]
	hooks       atomic.Pointer[hooks.Manager]
	// procs is the current generation's sandbox process feed (the
	// conversation-scoped tap the activity card reads back). Rebound on
	// every runtime reload; nil when the deployment has no feed.
	procs         atomic.Pointer[sandbox.ProcessFeed]
	usage         func(context.Context, inference.Usage)
	usageRecorder UsageRecorder

	mu       sync.Mutex
	runs     map[RunID]*runDetail
	runsCond *sync.Cond
	// startGates serializes run starts per conversation (see
	// lockStart). Entries live for the Host's lifetime; a Host is
	// retired with its runtime generation, so the map cannot grow past
	// the conversations started on one generation.
	startGates map[ConversationID]*sync.Mutex
	rollouts   map[ConversationID]*rollout.Recorder
	titling    map[ConversationID]bool
	titleWG    sync.WaitGroup
	// rebindMu serializes onRuntimeReload. ReloadDocument rebinds
	// synchronously before returning while the runtime event router
	// may dispatch the same rebuild event concurrently, and both must
	// never interleave resource Bind/LoadAll side effects for
	// different generations.
	rebindMu sync.Mutex
	// deleting marks one conversation whose removal is in flight.
	// StartRun refuses new runs for it and DeleteConversation cancels
	// every run the Host already owns for it.
	deleting map[ConversationID]bool
	// deleted tombstones conversations whose rows were removed for the
	// lifetime of this Host, so a stale explicit StartRun cannot mint
	// the same conversation id again.
	deleted map[ConversationID]bool
	// importMu serializes archive write + memory seed across callers
	// so a duplicate import with the same Source cannot double-seed.
	importMu   sync.Mutex
	artifact   func(context.Context, string, []byte)
	sessionUpd func(context.Context, string)
	// stale records a Manager retirement on the Host itself. The pool
	// entry carries the same flag while the Host is pooled; the
	// Host-level copy survives pool removal so adapters can still tell
	// a draining Host from a fresh replacement handed out by Acquire.
	stale   atomic.Bool
	closing bool
	closed  bool
	// closeDone is closed once a drained host has finished teardown;
	// active runs wait on it before returning so the shared session
	// store outlives every post-run write (including auto titles).
	closeDone chan struct{}
	// recovery is the summary of the crash-recovery pass this Host ran
	// at assembly (see recover.go). Guarded by mu.
	recovery RecoveryReport
	// reflow is this Host's subscription to the delegation board
	// (see reflow.go). It is replaced on every runtime reload, so it
	// has its own lock instead of riding mu: the watcher loop never
	// touches Host state under it.
	reflowMu sync.Mutex
	reflow   *reflowWatch
}

// RunID identifies one engine run inside a Host.
type RunID string

// ConversationID identifies one conversation inside a Host.
type ConversationID string

// runDetail is the internal per-run state owned by Host.
type runDetail struct {
	run *Run

	contextID string
	usage     sessions.Usage
	// usageHours aggregates engine reports by model + UTC hour so a
	// multi-model or long turn still lands in the right user-level
	// statistics buckets.
	usageHours map[string]sessions.Usage
	notify     func(context.Context, inference.Usage)
	buffer     *rolloutBuffer
	backend    interact.Backend
	// requestID/responseID hold the provider correlation identifiers
	// of the run's final generation, captured from the terminal
	// stream finish delta. Guarded by Host.mu.
	requestID  string
	responseID string
	// steerSizes holds the text size of each steer message accepted
	// for this run and not yet observed as drained, oldest first. It
	// is what bounds the payload one round boundary can inject (see
	// maxSteerQueuedBytes and steerQueuedBytes). Guarded by Host.mu.
	steerSizes []int
	// onSteer reports a boundary draining this run's steer queue (see
	// RunOptions.OnSteerPending). Guarded by Host.mu.
	onSteer func(ctx context.Context, runID string, pending int)
}

// dropRun removes an ended run from the active set. Usage for the run
// must already have been captured before this is called.
func (h *Host) dropRun(runID RunID) {
	h.mu.Lock()
	delete(h.runs, runID)
	idle := len(h.runs) == 0
	h.runsCond.Broadcast()
	h.mu.Unlock()
	if idle {
		if m := h.manager; m != nil {
			m.hostIdle(h)
		}
	}
}

// hasActiveRuns reports whether the Host still owns live runs.
func (h *Host) hasActiveRuns() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runs) > 0
}

// liveRunFor reports the id of a run this Host still owns for one
// conversation, if any. A run stays owned until its Wait has run the
// settle bookkeeping, so a conversation whose turn just settled but was
// never waited on still reads as busy — the next automation occurrence
// retries, which is the conservative direction.
func (h *Host) liveRunFor(id ConversationID) (RunID, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for runID, detail := range h.runs {
		if detail != nil && ConversationID(detail.contextID) == id {
			return runID, true
		}
	}
	return "", false
}

// lockStart serializes run starts that share one conversation: the
// conflict decision (liveRunFor) and the registration of the new run
// must be one step, or two starts can both decide "no live run" and the
// later one preempts the earlier. Starts of different conversations do
// not contend; the returned function releases the lock.
func (h *Host) lockStart(id ConversationID) func() {
	h.mu.Lock()
	if h.startGates == nil {
		h.startGates = make(map[ConversationID]*sync.Mutex)
	}
	gate := h.startGates[id]
	if gate == nil {
		gate = &sync.Mutex{}
		h.startGates[id] = gate
	}
	h.mu.Unlock()
	gate.Lock()
	return gate.Unlock
}

// markStale records that the Manager retired this Host: it keeps
// serving new turns on its old runtime until the last active run ends,
// then closes itself through hostIdle.
func (h *Host) markStale() {
	h.stale.Store(true)
}

// IsStale reports whether the Manager retired this Host. Callers that
// receive a stale Host from Acquire must schedule a replacement for
// after teardown so the pool (and the adapter's current Host) never
// stays pinned to a closed runtime.
func (h *Host) IsStale() bool {
	return h != nil && h.stale.Load()
}

// IsClosing reports whether the Host stopped accepting new turns: it
// is either draining its live runs or already torn down. A closing
// Host is not handed out by Manager.Ensure, and Manager.Current keeps
// reporting it while it drains, so an adapter reads IsClosing on what
// it holds as "this generation is on its way out" — the cue to ask the
// pool for the workspace's Host again.
func (h *Host) IsClosing() bool {
	if h == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing || h.closed
}

// WaitClosed blocks until the Host has fully torn down (or ctx is
// canceled). A nil or already-closed Host returns immediately.
func (h *Host) WaitClosed(ctx context.Context) error {
	if h == nil {
		return nil
	}
	for {
		h.mu.Lock()
		closed := h.closed
		closeDone := h.closeDone
		h.mu.Unlock()
		if closed || closeDone == nil {
			return nil
		}
		select {
		case <-closeDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (h *Host) waitClosed(ctx context.Context) error {
	return h.WaitClosed(ctx)
}

// RunView is the read-only identity of one active run.
type RunView struct {
	RunID          string
	ConversationID string
}

// ActiveRuns snapshots every live run owned by the Host.
func (h *Host) ActiveRuns() []RunView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RunView, 0, len(h.runs))
	for id, d := range h.runs {
		out = append(out, RunView{
			RunID:          string(id),
			ConversationID: d.contextID,
		})
	}
	return out
}

// backendForRun selects the prompt backend bound to one run. Nil means
// the Host's fallback backend applies.
func (h *Host) backendForRun(runID string) interact.Backend {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := h.runs[RunID(runID)]; d != nil {
		return d.backend
	}
	return nil
}

// SetArtifactObserver installs a callback invoked for every observed
// workspace write before the artifact is buffered.
func (h *Host) SetArtifactObserver(fn func(context.Context, string, []byte)) {
	h.mu.Lock()
	h.artifact = fn
	h.mu.Unlock()
}

// SetSessionUpdated installs a callback fired when a conversation
// title changes.
func (h *Host) SetSessionUpdated(fn func(context.Context, string)) {
	h.mu.Lock()
	h.sessionUpd = fn
	h.mu.Unlock()
}

// SessionsStore returns the workspace conversation store backing this Host,
// or nil when the Host is not bound yet.
func (h *Host) SessionsStore() *sessions.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// notifySessionUpdated fires the installed session-title callback
// without holding h.mu while the callback runs.
func (h *Host) notifySessionUpdated(
	ctx context.Context, contextID string,
) {
	h.mu.Lock()
	fn := h.sessionUpd
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, contextID)
	}
}

// rolloutBuffer accumulates one run's assistant text/reasoning until
// the stream finish delta.
type rolloutBuffer struct {
	text      strings.Builder
	reasoning strings.Builder
}

// WorkDir returns the workspace path.
func (h *Host) WorkDir() string { return h.workDir }

// Sessions returns the shared conversation store.
func (h *Host) Sessions() *sessions.Store { return h.store }

// Controller returns the flowcraft runtime lifecycle controller.
func (h *Host) Controller() *engine.Controller { return h.ctrl }

// Broker returns the run-routed prompt broker.
func (h *Host) Broker() *interact.Broker { return h.broker }

// Agents returns the runtime's agent lifecycle registry, or nil when
// the runtime does not wire one.
func (h *Host) Agents() *ocsagents.Lifecycle { return h.agents.Load() }

// Processes returns the sandboxed child processes the given
// conversation started on the current runtime generation, oldest
// first. Empty when the deployment wires no feed.
func (h *Host) Processes(conversationID string) []sandbox.Process {
	feed := h.procs.Load()
	if feed == nil {
		return nil
	}
	return feed.List(conversationID)
}

// CancelRun cancels one live engine turn. It returns an error when the
// run is not active on this Host.
func (h *Host) CancelRun(runID string) error {
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	h.mu.Unlock()
	if d == nil || d.run == nil || d.run.turn == nil {
		return errors.New("host: turn not found")
	}
	d.run.turn.Cancel()
	return nil
}

// CancelAll cancels every live run on this Host.
func (h *Host) CancelAll() {
	h.mu.Lock()
	runs := make([]*Run, 0, len(h.runs))
	for _, d := range h.runs {
		if d != nil && d.run != nil {
			runs = append(runs, d.run)
		}
	}
	h.mu.Unlock()
	for _, r := range runs {
		if r != nil && r.turn != nil {
			r.turn.Cancel()
		}
	}
}

// Close releases the Host. When the last Host for a workspace closes,
// its runtime and session store are torn down; hosts with active runs
// are drained in the background and only torn down after every turn
// finishes naturally.
func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	m := h.manager
	if m == nil {
		return nil
	}
	m.mu.Lock()
	ref := m.hosts[h.workDir]
	if ref != nil && ref.host != h {
		m.mu.Unlock()
		return nil
	}
	if ref != nil {
		ref.refs--
		if ref.refs > 0 {
			m.mu.Unlock()
			return nil
		}
		delete(m.hosts, h.workDir)
		if m.retiring == nil {
			m.retiring = make(map[string]*Host)
		}
		m.retiring[h.workDir] = h
	}
	m.mu.Unlock()

	h.beginClose()
	return nil
}

// beginClose marks the Host as closing and starts teardown. Idle hosts
// drain and close synchronously; hosts with active runs drain in the
// background while the old runtime keeps serving their turns.
func (h *Host) beginClose() {
	h.mu.Lock()
	if h.closed || h.closing {
		h.mu.Unlock()
		return
	}
	h.closing = true
	active := len(h.runs) > 0
	h.mu.Unlock()
	if active {
		go h.closeWhenDrained()
		return
	}
	h.closeWhenDrained()
}

// closeWhenDrained waits for active runtime sessions through
// flowcraft's Drain API, then releases broker, runtime, and store.
// Drain never interrupts turns, so background streams finish first.
func (h *Host) closeWhenDrained() {
	ctx := context.WithoutCancel(context.Background())
	telemetry.WarnErr(ctx, "host: drain before close failed", h.drain(ctx))
	h.doClose()
}

func (h *Host) drain(ctx context.Context) error {
	ctrl := h.Controller()
	if ctrl == nil {
		return nil
	}
	return ctrl.Drain(ctx)
}

func (h *Host) doClose() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	// Run.Wait registers the post-run auto-title before it drops the
	// run from the active set, so waiting for the set to drain here
	// guarantees titleWG.Wait below can never observe an empty group
	// before that registration happens. flowcraft's drain returns as
	// soon as the engine turn ends, which can precede Run.Wait's own
	// bookkeeping; titleWG.Wait alone would then tear the shared store
	// down under the late title write.
	for len(h.runs) > 0 {
		h.runsCond.Wait()
	}
	h.closed = true
	closeDone := h.closeDone
	h.mu.Unlock()
	// Auto-titles borrow the runtime and shared session store after a
	// turn ends. Wait for them before tearing either down so a title
	// never races the DB close.
	h.titleWG.Wait()
	h.closeRollouts()
	h.detachReflow()
	h.broker.Close()
	telemetry.WarnErr(context.Background(), "host: close controller failed",
		h.ctrl.Close())
	if h.manager != nil {
		h.manager.releaseStore(h.store)
	}
	if closeDone != nil {
		close(closeDone)
	}
	if h.manager != nil {
		h.manager.hostClosed(h.workDir, h)
	}
}

// awaitCloseIfClosing blocks the final run wait on the host teardown
// when the host was invalidated mid-turn. Returning before teardown
// would let callers resume the workspace while the shared store was
// still owned by this host's post-run writes.
func (h *Host) awaitCloseIfClosing() {
	h.mu.Lock()
	closing := h.closing && !h.closed
	closeDone := h.closeDone
	h.mu.Unlock()
	if closing && closeDone != nil {
		<-closeDone
	}
}
