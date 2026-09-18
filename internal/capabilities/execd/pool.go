package execd

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// PoolSettings are the user-visible pool knobs.
type PoolSettings struct {
	// Prewarm is how many idle (unbound) children are kept ready.
	Prewarm int `json:"prewarm"`
	// MaxIdle caps how many unbound children the pool keeps; children
	// released beyond it are stopped.
	MaxIdle int `json:"maxIdle"`
	// MaxActive caps how many workspaces hold a pooled child at once.
	// A lease beyond the cap still works: it gets a dedicated child
	// that is stopped when its runner closes instead of being reused.
	MaxActive int `json:"maxActive"`
	// IdleTTL closes idle children down to Prewarm after this long.
	IdleTTL time.Duration `json:"idleTtl"`
}

// DefaultPoolSettings returns the shipped defaults.
func DefaultPoolSettings() PoolSettings {
	return PoolSettings{
		Prewarm:   1,
		MaxIdle:   4,
		MaxActive: 16,
		IdleTTL:   5 * time.Minute,
	}
}

// NormalizePoolSettings repairs out-of-range values. A value above the
// documented maximum is clamped to it (9 pre-warm children become 8
// rather than silently snapping back to the default), and the knobs
// that have no meaningful zero fall back to the shipped defaults - the
// same shape an absent `exec` section produces.
func NormalizePoolSettings(s PoolSettings) PoolSettings {
	def := DefaultPoolSettings()
	if s.Prewarm < 0 {
		s.Prewarm = 0
	}
	if s.Prewarm > 8 {
		s.Prewarm = 8
	}
	if s.MaxIdle < s.Prewarm {
		s.MaxIdle = s.Prewarm
	}
	if s.MaxIdle > 16 {
		s.MaxIdle = 16
	}
	if s.MaxActive <= 0 {
		s.MaxActive = def.MaxActive
	}
	if s.MaxActive > 64 {
		s.MaxActive = 64
	}
	if s.IdleTTL <= 0 {
		s.IdleTTL = def.IdleTTL
	}
	if s.IdleTTL > time.Hour {
		s.IdleTTL = time.Hour
	}
	return s
}

// SetSettings applies new pool settings to future leases and reaping,
// trims idle children that exceed the new MaxIdle and re-arms the
// reaper so a shorter IdleTTL takes effect without a restart.
func (p *Pool) SetSettings(settings PoolSettings) {
	p.mu.Lock()
	p.settings = NormalizePoolSettings(settings)
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return
	}
	select {
	case p.settingsCh <- struct{}{}:
	default:
	}
}

type idleChild struct {
	client *Client
	stop   func()
	since  time.Time
}

// Pool owns pre-warmed exec children and leases them to workspaces.
// One lease is one workspace: the child is bound for the lease's
// lifetime and returned unbound when the runner closes.
type Pool struct {
	settings PoolSettings
	launcher func(ctx context.Context) (*Client, func(), error)

	// baseCtx bounds background launches: Close cancels it, so a fork
	// that is still in flight cannot delay shutdown for a handshake.
	baseCtx context.Context
	cancel  context.CancelFunc

	mu         sync.Mutex
	idle       []*idleChild
	active     int
	used       bool
	closed     bool
	closeOnce  sync.Once
	stopCh     chan struct{}
	settingsCh chan struct{}
	warmCh     chan struct{}
	wg         sync.WaitGroup
}

// NewPool creates a pool and starts its janitor loop. No child is
// forked before the first lease: a deployment that never runs a
// sandboxed command (remote: false) pays nothing, and a pool that is
// used tops itself back up to Prewarm.
func NewPool(settings PoolSettings) *Pool {
	baseCtx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		settings: NormalizePoolSettings(settings),
		launcher: func(ctx context.Context) (*Client, func(), error) {
			return Launch(ctx)
		},
		baseCtx:    baseCtx,
		cancel:     cancel,
		stopCh:     make(chan struct{}),
		settingsCh: make(chan struct{}, 1),
		warmCh:     make(chan struct{}, 1),
	}
	p.wg.Add(1)
	go p.loop()
	return p
}

// SetLauncher overrides the child factory. Tests use it to inject
// in-process pairs.
func (p *Pool) SetLauncher(fn func(ctx context.Context) (*Client, func(), error)) {
	if fn == nil {
		return
	}
	p.mu.Lock()
	p.launcher = fn
	p.mu.Unlock()
}

// Settings returns the normalized settings.
func (p *Pool) Settings() PoolSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// Stats reports idle/active child counts.
func (p *Pool) Stats() (idle, active int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle), p.active
}

// Lease returns a runner bound to workdir/policy. A lease beyond
// MaxActive gets a dedicated child instead of being refused: the cap
// bounds children the pool reuses, it must not fail a workspace.
func (p *Pool) Lease(
	ctx context.Context,
	workdir string,
	policy *SandboxPolicy,
) (*RemoteRunner, error) {
	child, pooled, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var opts []RemoteOption
	if pooled {
		opts = append(opts, withRelease(func() { p.releaseChild(child) }))
	}
	runner, err := NewRemoteRunner(
		ctx, child.client, child.stop, workdir, policy, opts...)
	if err != nil {
		// NewRemoteRunner already stopped the child when the bind
		// failed; only the pooled slot has to come back.
		if pooled {
			p.dropSlot()
		}
		return nil, err
	}
	p.signalWarm()
	return runner, nil
}

// signalWarm arms the pre-warm loop: from the first lease onwards the
// pool keeps Prewarm children ready.
func (p *Pool) signalWarm() {
	p.mu.Lock()
	p.used = true
	p.mu.Unlock()
	select {
	case p.warmCh <- struct{}{}:
	default:
	}
}

// acquire returns an idle child (pooled) or a dedicated one when the
// pool is at capacity (not pooled).
func (p *Pool) acquire(ctx context.Context) (*idleChild, bool, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false, errdefs.NotAvailablef("execd: pool is closed")
	}
	// Take the freshest idle child; expired ones are stopped as we go.
	now := time.Now()
	for len(p.idle) > 0 {
		child := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if now.Sub(child.since) < p.settings.IdleTTL {
			p.active++
			p.mu.Unlock()
			return child, true, nil
		}
		p.mu.Unlock()
		child.stop()
		p.mu.Lock()
	}
	launcher := p.launcher
	if p.active >= p.settings.MaxActive {
		maxActive := p.settings.MaxActive
		p.mu.Unlock()
		telemetry.Warn(ctx,
			"execd: pool at capacity; using a dedicated child for this workspace",
			otellog.Int("execd.max_active", maxActive))
		client, stop, err := launcher(ctx)
		if err != nil {
			return nil, false, err
		}
		return &idleChild{client: client, stop: stop, since: time.Now()},
			false, nil
	}
	p.active++
	p.mu.Unlock()

	client, stop, err := launcher(ctx)
	if err != nil {
		p.dropSlot()
		return nil, false, err
	}
	return &idleChild{client: client, stop: stop, since: time.Now()}, true, nil
}

func (p *Pool) dropSlot() {
	p.mu.Lock()
	if p.active > 0 {
		p.active--
	}
	p.mu.Unlock()
}

// releaseChild returns a leased child to the idle pool, or stops it when
// the pool is full/closed or the child is no longer healthy.
func (p *Pool) releaseChild(child *idleChild) {
	p.mu.Lock()
	if p.active > 0 {
		p.active--
	}
	closed := p.closed
	full := len(p.idle) >= p.settings.MaxIdle
	p.mu.Unlock()
	if closed || full {
		child.stop()
		return
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := child.client.Ping(pingCtx)
	cancel()
	if err != nil {
		child.stop()
		return
	}
	p.mu.Lock()
	if p.closed || len(p.idle) >= p.settings.MaxIdle {
		p.mu.Unlock()
		child.stop()
		return
	}
	child.since = time.Now()
	p.idle = append(p.idle, child)
	p.mu.Unlock()
}

// loop reaps idle children, tops the pool back up to Prewarm and reacts
// to settings changes. It forks nothing until the first lease armed it.
func (p *Pool) loop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.reapInterval())
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-p.settingsCh:
			ticker.Reset(p.reapInterval())
			p.trimIdle()
		case <-p.warmCh:
			p.prewarm()
		case <-ticker.C:
			p.reapIdle()
			p.prewarm()
		}
	}
}

// reapInterval is the reaper cadence of the current settings.
func (p *Pool) reapInterval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	interval := p.settings.IdleTTL / 4
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	return interval
}

// trimIdle stops the newest idle children that exceed MaxIdle. The
// oldest Prewarm entries are the ones the reaper protects and the ones
// a lease takes last, so they are the ones kept.
func (p *Pool) trimIdle() {
	p.mu.Lock()
	var stop []*idleChild
	for len(p.idle) > p.settings.MaxIdle {
		child := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		stop = append(stop, child)
	}
	p.mu.Unlock()
	for _, child := range stop {
		child.stop()
	}
}

func (p *Pool) reapIdle() {
	p.mu.Lock()
	now := time.Now()
	kept := p.idle[:0]
	var expired []*idleChild
	for i, child := range p.idle {
		if i < p.settings.Prewarm || now.Sub(child.since) < p.settings.IdleTTL {
			kept = append(kept, child)
			continue
		}
		expired = append(expired, child)
	}
	p.idle = kept
	p.mu.Unlock()
	for _, child := range expired {
		child.stop()
	}
}

func (p *Pool) prewarm() {
	for {
		p.mu.Lock()
		needed := 0
		// A warm child is only useful while another workspace could
		// still lease it: at the active cap every new lease gets a
		// dedicated child anyway.
		if p.used && p.active < p.settings.MaxActive {
			needed = p.settings.Prewarm - len(p.idle)
		}
		closed := p.closed
		p.mu.Unlock()
		if closed || needed <= 0 {
			return
		}
		client, stop, err := p.launchBackground()
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"execd: prewarm child failed", err)
			return
		}
		p.mu.Lock()
		if p.closed || len(p.idle) >= p.settings.MaxIdle {
			p.mu.Unlock()
			stop()
			return
		}
		p.idle = append(p.idle, &idleChild{
			client: client,
			stop:   stop,
			since:  time.Now(),
		})
		p.mu.Unlock()
	}
}

func (p *Pool) launchBackground() (*Client, func(), error) {
	p.mu.Lock()
	launcher := p.launcher
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(p.baseCtx, handshakeTimeout)
	defer cancel()
	return launcher(ctx)
}

// Close stops every idle child, rejects new leases and aborts a launch
// that is still in flight so shutdown does not wait out a handshake.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.stopCh)
		p.cancel()
		p.mu.Lock()
		p.closed = true
		idle := p.idle
		p.idle = nil
		p.mu.Unlock()
		for _, child := range idle {
			child.stop()
		}
		p.wg.Wait()
	})
}

// defaultPool is the process-wide pool the sandbox resource uses when
// one is configured; without it the sandbox launches children lazily.
var defaultPool atomic.Pointer[Pool]

// SetDefaultPool installs the process-wide pool.
func SetDefaultPool(pool *Pool) { defaultPool.Store(pool) }

// DefaultPool returns the process-wide pool, or nil.
func DefaultPool() *Pool { return defaultPool.Load() }
