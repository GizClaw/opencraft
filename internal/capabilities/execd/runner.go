// Package execd is opencraft's exec supervisor transport: the child
// process serves the protobuf protocol in server.go, and the parent
// side implements sandbox.Runner here so callers never see the wire.
package execd

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/rs/xid"
)

const (
	// defaultReadWaitMs is the long-poll window a remote read uses. The
	// server holds the read for at most this long, so a quiet command
	// costs two round trips a second instead of a hot poll loop.
	//
	// This is a deliberate difference from the in-process runner: a
	// local Read blocks until data arrives or the caller's context
	// ends, while a remote Read whose window lapses answers with an
	// empty, non-EOF result the caller polls again from.
	defaultReadWaitMs = 500
	// closeTimeout bounds the force-terminate and release RPCs a
	// sandbox.Session.Close sends. Close is terminal: it must never
	// hang a caller when the child is wedged.
	closeTimeout = 5 * time.Second
	// closeInputTimeout bounds CloseInput. It outlasts a long-poll read
	// already queued ahead of it in the process's actor, and still
	// bounds a child that stopped answering.
	closeInputTimeout = maxReadWaitMs*time.Millisecond + closeTimeout
)

// Watchdog tuning defaults. Tests shrink these per runner through
// withWatchdog instead of mutating package state.
const (
	watchdogPingInterval = 10 * time.Second
	watchdogPingTimeout  = 5 * time.Second
	watchdogMaxFailures  = 3
	// maxWatchdogRestarts bounds how many restart cycles the watchdog
	// attempts before it stops and leaves recovery to a runtime reload.
	maxWatchdogRestarts = 3
)

// relaunchProbeTimeout bounds the capabilities probe a relaunched child
// must answer before the runner trusts it.
const relaunchProbeTimeout = 5 * time.Second

// defaultRelaunchBackoff is the wait before each relaunch attempt. A
// child that keeps dying must not turn into a fork bomb.
var defaultRelaunchBackoff = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
}

// RemoteRunner is a sandbox.Runner backed by an execd child leased for
// one workspace. The child stays bound to workdir/policy for the
// runner's lifetime; Close releases the lease back to the pool (or
// closes the child when no pool owns it).
type RemoteRunner struct {
	mu      sync.Mutex
	client  *Client
	stop    func()
	release func()
	// leased is the child the pool handed over. The pool owns it and
	// release returns it; a watchdog relaunch replaces client/stop with
	// a child this runner owns, so Close has to stop that one itself.
	leased   *Client
	workdir  string
	policy   *SandboxPolicy
	caps     sandbox.Capabilities
	modeFn   func(context.Context) bool
	relaunch func() (*Client, func(), error)
	bound    bool
	dead     bool
	closed   bool

	relaunchMu sync.Mutex
	watchStop  chan struct{}
	watchDone  chan struct{}
	closeOnce  sync.Once

	pingInterval time.Duration
	pingTimeout  time.Duration
	maxFailures  int
	backoff      []time.Duration

	stats runnerStats
}

type runnerStats struct {
	pingFailures atomic.Int64
	restarts     atomic.Int64
}

// RemoteRunnerStats is a snapshot of the runner's resilience counters.
type RemoteRunnerStats struct {
	PingFailures int64 `json:"pingFailures"`
	Restarts     int64 `json:"restarts"`
	Dead         bool  `json:"dead"`
}

// RemoteOption configures a RemoteRunner at construction.
type RemoteOption func(*RemoteRunner)

// withWatchdog overrides the ping cadence and relaunch backoff. It is
// unexported: production keeps the defaults, tests shrink the windows.
func withWatchdog(
	interval, timeout time.Duration,
	maxFailures int,
	backoff []time.Duration,
) RemoteOption {
	return func(r *RemoteRunner) {
		r.pingInterval = interval
		r.pingTimeout = timeout
		r.maxFailures = maxFailures
		r.backoff = backoff
	}
}

// withRelease installs the lease-return hook the pool uses.
func withRelease(fn func()) RemoteOption {
	return func(r *RemoteRunner) { r.release = fn }
}

// NewRemoteRunner binds the child to workdir/policy and returns a
// runner. stop terminates the child; release (optional) returns the
// lease to a pool.
func NewRemoteRunner(
	ctx context.Context,
	client *Client,
	stop func(),
	workdir string,
	policy *SandboxPolicy,
	opts ...RemoteOption,
) (*RemoteRunner, error) {
	if client == nil {
		return nil, errdefs.Validationf("execd: nil client")
	}
	runner := &RemoteRunner{
		client:       client,
		stop:         stop,
		leased:       client,
		workdir:      workdir,
		policy:       policy,
		watchStop:    make(chan struct{}),
		watchDone:    make(chan struct{}),
		pingInterval: watchdogPingInterval,
		pingTimeout:  watchdogPingTimeout,
		maxFailures:  watchdogMaxFailures,
		backoff:      defaultRelaunchBackoff,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(runner)
		}
	}
	runner.caps = capabilitiesFromHello(client.HelloInfo())
	if _, err := client.Bind(ctx, workdir, policy); err != nil {
		if stop != nil {
			stop()
		}
		return nil, err
	}
	runner.bound = true
	go runner.watch(context.WithoutCancel(ctx))
	return runner, nil
}

func capabilitiesFromHello(hello *HelloOk) sandbox.Capabilities {
	features := sandbox.SessionFeatures{}
	for _, capability := range hello.GetCapabilities() {
		switch capability {
		case "pty":
			features.TTY = true
		case "signal":
			features.Signal = true
		}
	}
	return sandbox.Capabilities{Features: features}
}

// SetModeFunc wires a per-request YOLO resolver: it receives the start
// request's context (which carries the session identity) and reports
// whether the command should run unconfined.
func (r *RemoteRunner) SetModeFunc(fn func(context.Context) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modeFn = fn
}

// SetRelauncher installs the factory the watchdog uses to fork a fresh
// execd child after the current one died. Without one the runner stays
// dead until the owning runtime is rebuilt.
func (r *RemoteRunner) SetRelauncher(fn func() (*Client, func(), error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relaunch = fn
}

// Stats returns the current runner counters.
func (r *RemoteRunner) Stats() RemoteRunnerStats {
	r.mu.Lock()
	dead := r.dead
	r.mu.Unlock()
	return RemoteRunnerStats{
		PingFailures: r.stats.pingFailures.Load(),
		Restarts:     r.stats.restarts.Load(),
		Dead:         dead,
	}
}

// available returns the bound child client or a clear error.
func (r *RemoteRunner) available() (*Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || r.dead {
		return nil, errdefs.NotAvailablef(
			"execd: sandbox child is unavailable; the runtime reloads it in the background")
	}
	return r.client, nil
}

// ensureBound returns a client that is bound to this runner's
// workspace, rebinding after a watchdog restart.
func (r *RemoteRunner) ensureBound(ctx context.Context) (*Client, error) {
	client, err := r.available()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	bound := r.bound
	r.mu.Unlock()
	if bound {
		return client, nil
	}
	if _, err := client.Bind(ctx, r.workdir, r.policy); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.bound = true
	r.mu.Unlock()
	return client, nil
}

// ping probes the child; the watchdog calls it on a timer.
func (r *RemoteRunner) ping(ctx context.Context) error {
	client, err := r.available()
	if err != nil {
		return err
	}
	_, err = client.Ping(ctx)
	return err
}

// watch pings the child and relaunches it after repeated failures, so a
// wedged or crashed child cannot take the workspace's tools down until
// the next runtime reload.
func (r *RemoteRunner) watch(ctx context.Context) {
	defer close(r.watchDone)
	interval := r.pingInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	failedRestarts := 0
	for {
		select {
		case <-r.watchStop:
			return
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, r.pingTimeout)
		err := r.ping(pingCtx)
		cancel()
		if err == nil {
			failures = 0
			continue
		}
		failures++
		if failures < r.maxFailures {
			continue
		}
		failures = 0
		r.stats.pingFailures.Add(1)
		telemetry.WarnErr(context.Background(),
			"execd: sandbox child unresponsive; restarting", err)
		if err := r.restart(ctx); err != nil {
			failedRestarts++
			telemetry.WarnErr(context.Background(),
				"execd: sandbox child restart failed", err)
			if failedRestarts >= maxWatchdogRestarts {
				telemetry.Warn(context.Background(),
					"execd: giving up on the sandbox child; a runtime reload is required")
				return
			}
			continue
		}
		failedRestarts = 0
	}
}

// restart drops the current child and relaunches it with backoff.
func (r *RemoteRunner) restart(ctx context.Context) error {
	r.relaunchMu.Lock()
	defer r.relaunchMu.Unlock()

	r.mu.Lock()
	client, stop, relaunch, closed := r.client, r.stop, r.relaunch, r.closed
	if !closed {
		r.client, r.stop, r.dead, r.bound = nil, nil, true, false
	}
	r.mu.Unlock()
	if closed {
		return nil
	}
	if client != nil {
		telemetry.WarnErr(context.Background(),
			"execd: close unresponsive child client failed", client.Close())
	}
	if stop != nil {
		stop()
	}
	if relaunch == nil {
		return errdefs.NotAvailablef("execd: no relauncher configured")
	}
	for _, backoff := range r.backoff {
		select {
		case <-r.watchStop:
			return nil
		case <-time.After(backoff):
		}
		newClient, newStop, err := relaunch()
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"execd: relaunch child failed", err)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, relaunchProbeTimeout)
		_, err = newClient.Ping(probeCtx)
		cancel()
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"execd: relaunched child failed its probe", err)
			if newStop != nil {
				newStop()
			}
			continue
		}
		r.mu.Lock()
		closed = r.closed
		r.mu.Unlock()
		if closed {
			telemetry.WarnErr(context.Background(),
				"execd: close discarded child client failed", newClient.Close())
			if newStop != nil {
				newStop()
			}
			return nil
		}
		r.mu.Lock()
		r.client = newClient
		r.stop = newStop
		r.dead = false
		r.bound = false
		r.caps = capabilitiesFromHello(newClient.HelloInfo())
		r.mu.Unlock()
		r.stats.restarts.Add(1)
		telemetry.Info(context.Background(), "execd: sandbox child restarted")
		return nil
	}
	return errdefs.NotAvailablef("execd: relaunch failed after %d attempts",
		len(r.backoff))
}

var _ sandbox.Runner = (*RemoteRunner)(nil)

// Capabilities reports the child server's session features.
func (r *RemoteRunner) Capabilities() sandbox.Capabilities {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.caps
}

// Start starts one session in the child.
func (r *RemoteRunner) Start(
	ctx context.Context,
	spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	if spec.ID == "" {
		spec.ID = xid.New().String()
	}
	client, err := r.ensureBound(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	modeFn := r.modeFn
	caps := r.caps
	r.mu.Unlock()

	sandboxOpts, err := execOptionsToProto(spec.Opts)
	if err != nil {
		return nil, err
	}
	resp, err := client.Start(ctx, &Start{
		ProcessId:  spec.ID,
		Argv:       spec.Argv,
		Env:        spec.Opts.Env.Inject,
		Tty:        spec.TTY,
		Rows:       int32(spec.Rows),
		Cols:       int32(spec.Cols),
		TimeoutMs:  spec.Opts.Timeout.Milliseconds(),
		Unconfined: modeFn != nil && modeFn(ctx),
		Sandbox:    sandboxOpts,
	})
	if err != nil {
		return nil, err
	}
	return &remoteSession{
		base:   context.WithoutCancel(ctx),
		client: client,
		id:     spec.ID,
		pid:    int(resp.GetPid()),
		caps: sandbox.SessionCapabilities{
			TTY:    spec.TTY && caps.Features.TTY,
			Signal: caps.Features.Signal,
		},
	}, nil
}

// List is not part of the execd protocol.
func (r *RemoteRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, errdefs.NotAvailablef("execd: session list is not supported")
}

// Terminate stops a session in the child (SIGTERM grace, then SIGKILL).
func (r *RemoteRunner) Terminate(ctx context.Context, id string) error {
	client, err := r.available()
	if err != nil {
		return err
	}
	_, err = client.Terminate(ctx, id, false)
	return err
}

// Close shuts the client down and releases the child: with a pool the
// child is unbound and returned to it, otherwise the process stops.
func (r *RemoteRunner) Close() error {
	r.closeOnce.Do(func() {
		select {
		case <-r.watchStop:
		default:
			close(r.watchStop)
		}
		select {
		case <-r.watchDone:
		case <-time.After(r.pingTimeout + time.Second):
		}
		r.mu.Lock()
		client, stop, release, leased := r.client, r.stop, r.release, r.leased
		r.client, r.stop, r.dead, r.closed = nil, nil, true, true
		r.mu.Unlock()
		if client != nil {
			unbindCtx, cancel := context.WithTimeout(
				context.Background(), closeTimeout)
			telemetry.WarnErr(context.Background(),
				"execd: unbind child on close failed", client.Unbind(unbindCtx))
			cancel()
		}
		if release != nil {
			// The pool owns the child it leased: it decides whether to
			// keep the unbound child warm or stop it. A child the
			// watchdog relaunched never entered the pool, so it is
			// stopped here instead of leaking until process exit.
			if client != nil && client != leased {
				telemetry.WarnErr(context.Background(),
					"execd: close relaunched child on release failed",
					client.Close())
				if stop != nil {
					stop()
				}
			}
			release()
			return
		}
		if client != nil {
			telemetry.WarnErr(context.Background(),
				"execd: close remote runner client failed", client.Close())
		}
		if stop != nil {
			stop()
		}
	})
	return nil
}

// remoteSession adapts the execd protocol to sandbox.Session.
type remoteSession struct {
	base   context.Context
	client *Client
	id     string
	pid    int
	caps   sandbox.SessionCapabilities

	mu     sync.Mutex
	exited bool
	exit   *sandbox.SessionExit
}

var _ sandbox.Session = (*remoteSession)(nil)

func (s *remoteSession) ID() string { return s.id }
func (s *remoteSession) PID() int   { return s.pid }
func (s *remoteSession) Capabilities() sandbox.SessionCapabilities {
	return s.caps
}

func (s *remoteSession) Read(
	ctx context.Context,
	afterSeq int64,
	maxBytes int,
) (sandbox.SessionOutput, error) {
	resp, err := s.client.Read(ctx, &Read{
		ProcessId: s.id,
		AfterSeq:  afterSeq,
		MaxBytes:  int32(maxBytes),
		WaitMs:    defaultReadWaitMs,
	})
	if err != nil {
		return sandbox.SessionOutput{}, err
	}
	return s.foldRead(resp), nil
}

func (s *remoteSession) Write(ctx context.Context, data []byte) error {
	ack, err := s.client.Write(ctx, &Write{
		ProcessId: s.id,
		Chunk:     data,
		WriteId:   xid.New().String(),
	})
	if err != nil {
		return err
	}
	switch ack.GetStatus() {
	case writeAccepted:
		return nil
	case writeStdinClosed:
		return sandbox.ErrSessionClosed
	default:
		return errdefs.NotAvailablef("execd: write status %q", ack.GetStatus())
	}
}

func (s *remoteSession) CloseInput() error {
	ctx, cancel := context.WithTimeout(s.base, closeInputTimeout)
	defer cancel()
	return s.client.CloseInput(ctx, s.id)
}

func (s *remoteSession) Resize(ctx context.Context, rows, cols int) error {
	return s.client.Resize(ctx, s.id, int32(rows), int32(cols))
}

func (s *remoteSession) Signal(ctx context.Context, _ sandbox.SessionSignal) error {
	return s.client.Signal(ctx, s.id, signalInterrupt)
}

func (s *remoteSession) Terminate(ctx context.Context) error {
	_, err := s.client.Terminate(ctx, s.id, false)
	return err
}

func (s *remoteSession) Wait(ctx context.Context) (sandbox.SessionExit, error) {
	resp, err := s.client.Wait(ctx, s.id)
	if err != nil {
		return sandbox.SessionExit{}, err
	}
	exit := sandbox.SessionExit{
		Code:   int(resp.GetExitCode()),
		Reason: exitReasonFromProto(resp.GetReason()),
	}
	s.mu.Lock()
	s.exited = true
	s.exit = &exit
	s.mu.Unlock()
	return exit, nil
}

func (s *remoteSession) foldRead(resp *ReadOk) sandbox.SessionOutput {
	chunks := make([]sandbox.OutputChunk, 0, len(resp.GetChunks()))
	for _, chunk := range resp.GetChunks() {
		chunks = append(chunks, sandbox.OutputChunk{
			Seq:    chunk.GetSeq(),
			Stream: streamFromProto(chunk.GetStream()),
			Data:   chunk.GetData(),
		})
	}
	out := sandbox.SessionOutput{
		NextSeq: resp.GetNextSeq(),
		Chunks:  chunks,
		EOF:     resp.GetEof(),
	}
	if resp.GetExited() {
		s.mu.Lock()
		if !s.exited {
			s.exited = true
			exit := sandbox.SessionExit{
				Code:   int(resp.GetExitCode()),
				Reason: exitReasonFromProto(resp.GetReason()),
			}
			s.exit = &exit
		}
		s.mu.Unlock()
	}
	return out
}

// Watch is not implemented on the client; output remains pullable
// through Read.
func (s *remoteSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	return nil, errdefs.NotAvailablef(
		"execd: watch is not implemented on the remote client")
}

// Close mirrors the local sandbox.Session contract: kill the process
// group immediately, then drop the server-side entry.
func (s *remoteSession) Close() error {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(s.base), closeTimeout)
	defer cancel()
	_, err := s.client.Terminate(ctx, s.id, true)
	if IsNotFound(err) {
		// The entry is already gone (the command exited and was reaped,
		// or Close ran before): closing is idempotent, so this is
		// success.
		err = nil
	}
	if _, relErr := s.client.Release(ctx, s.id); relErr != nil &&
		!IsNotFound(relErr) && !errors.Is(relErr, context.Canceled) &&
		!errors.Is(relErr, context.DeadlineExceeded) {
		telemetry.WarnErr(context.Background(),
			"execd: release closed session failed", relErr)
	}
	return err
}
