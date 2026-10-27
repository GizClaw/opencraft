// Package sandbox implements opencraft's per-session sandbox: a
// resource-ized host runner (HostSandbox) that switches between the
// confined chain (approval gate → env policy → seatbelt/bwrap/execd)
// and direct host execution (YOLO) based on the session's persisted
// permission mode, plus the workspace counterpart (HostWorkspace) and
// the execd child runner construction.
package sandbox

import (
	"context"
	"errors"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/telemetry"
	corenet "github.com/GizClaw/flowcraft/core/utils/netpolicy"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// execdPolicy renders the workspace sandbox policy for the execd wire.
func (s HostSandboxSettings) execdPolicy() *execd.SandboxPolicy {
	env := s.Env()
	return &execd.SandboxPolicy{
		WritablePaths: s.WritablePaths,
		EnvAllow:      env.Allow,
		EnvInject:     env.Inject,
		EnvAllowSet:   env.Allow != nil,
		CacheDir:      s.CacheDir,
	}
}

// Approver is the slice of the exec policy the confined chain needs:
// the approval decision plus the live allowlist it wraps.
type Approver interface {
	Approve(
		ctx context.Context,
		req coresandbox.ApprovalRequest,
	) (coresandbox.Decision, error)
	Allowlist() *coresandbox.Allowlist
}

// HostSandbox implements sandbox.Runner, switching between the
// confined chain (approval gate → env policy → OS sandbox) and the
// unconfined YOLO chain (direct host execution, full environment, no
// approvals) based on the current session's persisted permission mode.
// The mode is resolved per call from the execution context, so a mode
// switch applies to the next command immediately and other sessions
// are unaffected.
type HostSandbox struct {
	sessions   *sessions.Store
	confined   coresandbox.Runner
	unconfined coresandbox.Runner
	// escalator asks the user whether one command the OS sandbox
	// refused may run on the host instead. Nil turns escalation off:
	// headless and embedded deployments leave it unwired and keep a
	// refusal as a plain command failure.
	escalator coresandbox.Escalator
	// rules carries the workspace's persisted "always run without the
	// sandbox" rules (the exec policy). Nil remembers nothing.
	rules coresandbox.EscalationRules
	// procs taps every session started inside a conversation so the UI
	// can show the process tail. Nil when the deployment does not
	// declare the feed resource (embedded/headless test hosts).
	procs *ProcessFeed
}

// EscalationProvider is implemented by runners that can offer the
// escalation retry: it hands out the front end bound to this runner's
// chains and ask channel. It is a separate interface so tools keep
// depending on the plain sandbox.Runner.
type EscalationProvider interface {
	Escalation() *coresandbox.Escalation
}

// Unconfined reports whether the session in ctx runs unconfined
// (YOLO mode). It also satisfies execd's per-request resolver.
func (h *HostSandbox) Unconfined(ctx context.Context) bool {
	return IsYOLO(ctx, h.sessions)
}

func (h *HostSandbox) pick(ctx context.Context) coresandbox.Runner {
	if h.Unconfined(ctx) {
		return h.unconfined
	}
	return h.confined
}

func (h *HostSandbox) Close() error {
	// In remote mode the unconfined runner shares the confined backend
	// (its Close is a no-op wrapper), so ownership stays in confined.
	return errors.Join(h.confined.Close(), h.unconfined.Close())
}

func (h *HostSandbox) Capabilities() coresandbox.Capabilities {
	return h.confined.Capabilities()
}

func (h *HostSandbox) Start(
	ctx context.Context,
	spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	sess, err := h.start(ctx, spec)
	if err != nil {
		return nil, err
	}
	return h.procs.Tap(ctx, spec, sess), nil
}

// start picks the chain for this request and spawns the session.
func (h *HostSandbox) start(
	ctx context.Context,
	spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	if h.Unconfined(ctx) {
		return h.unconfined.Start(ctx, spec)
	}
	// Read-only mode narrows the per-call write policy before the
	// approval gate and the OS backend see it: the runner root is
	// dropped from the writable set for this command (explicit
	// writable paths like the cache stay writable). The approver sees
	// the same Opts, so it can auto-allow known read-only commands.
	// Read-only is also exempt from escalation: trading the mode's
	// workspace read-only guarantee for a per-command approval would
	// silently cancel the mode the user picked.
	if isReadOnly(ctx, h.sessions) {
		spec.Opts.Write = coresandbox.WriteReadOnly
	}
	// Remembered rules and approved retries never reach this path: the
	// escalation front end runs the retry through the unconfined chain
	// itself ([HostSandbox.Escalation]), and it is the only caller that
	// consults a rule — so a spawn this runner starts directly (an
	// interactive session, a tool that spawns its own process) is
	// confined by its session's mode and nothing else.
	return h.confined.Start(ctx, spec)
}

// escalationKey marks one start as an approved escalation retry. Locally
// the front end picks the unconfined chain directly, so the marker never
// decides a chain; it exists for the execd bridge, where the retry is a
// fresh start request on the same child and the wire carries the choice
// as a per-request flag.
type escalationKey struct{}

// escalated reports whether ctx marks a start as an approved escalation.
func escalated(ctx context.Context) bool {
	marked, _ := ctx.Value(escalationKey{}).(bool)
	return marked
}

// escalatingBridge marks every start it forwards as an approved
// escalation, so the execd child routes it to its own host runner. The
// child is a separate process: unlike the local chains, it cannot be
// handed the unconfined runner directly, only the request. Its Close is
// a no-op — it shares the confined chain's backend, which owns the
// close.
type escalatingBridge struct{ coresandbox.Runner }

func (escalatingBridge) Close() error { return nil }

func (b escalatingBridge) Start(
	ctx context.Context, spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	return b.Runner.Start(context.WithValue(ctx, escalationKey{}, true), spec)
}

// Escalation returns the escalation front end for the one-shot commands
// this runner serves, or nil when the deployment wired no escalator.
//
// The confined chain is the runner itself: its per-call pick already
// resolves the session's mode, so the first attempt is exactly the
// chain that mode asks for and the gate ([EscalationAvailable]) is what
// stops a YOLO or read-only session from being asked at all. The
// unconfined chain is the retry target — the host runner, or the same
// execd child with the request marked unconfined.
func (h *HostSandbox) Escalation() *coresandbox.Escalation {
	if h == nil || (h.escalator == nil && h.rules == nil) {
		return nil
	}
	return &coresandbox.Escalation{
		Confined:   h,
		Unconfined: h.unconfined,
		Escalate:   h.escalator,
		Rules:      h.rules,
		Gate:       h,
	}
}

// EscalationAvailable implements EscalationGate for the exec tools:
// only workspace mode has a confine worth leaving. Unconfined (YOLO)
// sessions never reach escalation because nothing refuses their
// commands, and read-only sessions must keep their guarantee.
func (h *HostSandbox) EscalationAvailable(ctx context.Context) bool {
	return !h.Unconfined(ctx) && !isReadOnly(ctx, h.sessions)
}

func (h *HostSandbox) List(
	ctx context.Context,
) ([]coresandbox.SessionInfo, error) {
	return h.pick(ctx).List(ctx)
}

func (h *HostSandbox) Terminate(ctx context.Context, id string) error {
	return h.pick(ctx).Terminate(ctx, id)
}

var _ coresandbox.Runner = (*HostSandbox)(nil)

// IsYOLO resolves the current session's permission mode from the
// execution context (flowcraft injects RunInfo during graph
// execution). Sessions without a persisted mode run confined. Exported
// so tool-level policies (e.g. the web_fetch gate) apply the same
// mode semantics.
func IsYOLO(ctx context.Context, store *sessions.Store) bool {
	mode, ok, err := sessions.ModeFromContext(ctx, store)
	if err != nil {
		// Unresolvable is not the same as unconfined: an unreadable row
		// keeps the sandbox on, and says so instead of silently
		// downgrading a yolo session.
		telemetry.WarnErr(ctx, "sandbox: resolve session mode failed", err,
			attribute.String(telemetry.AttrConversationID, conversationID(ctx)))
	}
	return ok && mode.IsYOLO()
}

// isReadOnly resolves the current session's read-only flag from the
// execution context, mirroring IsYOLO. Sessions without a persisted
// mode run workspace-write and return false.
func isReadOnly(ctx context.Context, store *sessions.Store) bool {
	mode, ok, err := sessions.ModeFromContext(ctx, store)
	if err != nil {
		telemetry.WarnErr(ctx, "sandbox: resolve session mode failed", err,
			attribute.String(telemetry.AttrConversationID, conversationID(ctx)))
	}
	return ok && mode.IsReadOnly()
}

// conversationID is the session the context belongs to, for log
// attributes. Empty when the context carries no run info.
func conversationID(ctx context.Context) string {
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		return info.ConversationID
	}
	return ""
}

// HostSandboxSettings is the deploy-document shape of the sandbox.Runner
// resource (impl opencraft): the workspace root, sandbox backends, and
// the environment policy applied in workspace mode.
type HostSandboxSettings struct {
	Root          string   `json:"root"`
	WritablePaths []string `json:"writable_paths,omitempty"`
	// CacheDir is the sandbox scratch root handed to the execd child
	// (Go/tmp build caches). The parent resolves it from the state root
	// (${ocraft:CACHE}); the child never resolves a user directory of
	// its own.
	CacheDir  string           `json:"cache_dir,omitempty"`
	Remote    bool             `json:"remote,omitempty"`
	EnvPolicy *EnvPolicyConfig `json:"env_policy,omitempty"`
}

// Env converts the configured environment policy into the sandbox
// EnvPolicy applied to every confined spawn. Both a missing section
// and a section without allow resolve to the curated default
// allowlist — nil would mean "inherit the whole host environment" to
// the core, which no configuration spelling should reach by accident
// (see EnvPolicyConfig.resolve).
func (s HostSandboxSettings) Env() coresandbox.EnvPolicy {
	return s.EnvPolicy.resolve()
}

// HostSandboxFactory builds the sandbox.Runner resource (impl
// opencraft) from settings plus the execpolicy and sessions deps.
type HostSandboxFactory struct{}

var _ resource.Factory = HostSandboxFactory{}

func (HostSandboxFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "sandbox.Runner",
		Impl: "opencraft",
		Deps: []resource.DepSpec{
			{Name: "execpolicy", Type: "opencraft.execpolicy", Required: true},
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
			{Name: "netpolicy", Type: NetPolicyResourceKind, Required: false},
			// Optional: the conversation-scoped process tap. Without
			// it sessions run exactly as before, untapped.
			{Name: "processes", Type: ProcessFeedResourceKind, Required: false},
		},
	}
}

func (HostSandboxFactory) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	s, err := resource.DecodeTyped[HostSandboxSettings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft sandbox: decode settings: %v", err)
	}
	if s.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft sandbox: settings.root is required")
	}
	approver, err := resourcedep.Required[Approver](
		in, "opencraft sandbox", "execpolicy")
	if err != nil {
		return nil, err
	}
	// The exec policy owns both halves of escalation: the persisted
	// rules the front end consults, and the prompt that answers one
	// refusal. Either half may be absent (a policy without a rule
	// store, a deployment without a user to ask), and absence turns
	// that half off instead of degrading into a silent retry.
	var (
		escalator coresandbox.Escalator
		rules     coresandbox.EscalationRules
	)
	if esc, ok := approver.(coresandbox.Escalator); ok {
		escalator = esc
	}
	if store, ok := approver.(coresandbox.EscalationRules); ok {
		rules = store
	}
	store, err := resourcedep.Required[*sessions.Store](
		in, "opencraft sandbox", "sessions")
	if err != nil {
		return nil, err
	}
	var feed *ProcessFeed
	if dep, ok := in.Dep("processes"); ok {
		if f, ok := dep.(*ProcessFeed); ok {
			feed = f
		}
	}
	// The configured exec network posture becomes the runner default:
	// WithDefaults pins Net onto every call, so deny-all / allow-list /
	// proxy apply to all sandboxed commands without touching each tool.
	var execNet corenet.NetPolicy
	if dep, ok := in.Dep("netpolicy"); ok {
		if pol, ok := dep.(Policy); ok {
			execNet = pol.Exec
		}
	}

	// The OS-level backend: platform sandbox locally, or the execd
	// child remotely (the child picks its own unconfined runner when a
	// start request carries Unconfined).
	var backend coresandbox.Runner
	if s.Remote {
		policy := s.execdPolicy()
		var (
			remote *execd.RemoteRunner
			err    error
		)
		if pool := execd.DefaultPool(); pool != nil {
			remote, err = pool.Lease(ctx, s.Root, policy)
		} else {
			var (
				client *execd.Client
				stop   func()
			)
			client, stop, err = execd.Launch(ctx)
			if err == nil {
				remote, err = execd.NewRemoteRunner(
					ctx, client, stop, s.Root, policy)
			}
		}
		if err != nil {
			return nil, err
		}
		// The watchdog restarts a dead or wedged child in the background;
		// the relaunch repeats the same fork handshake, and the runner
		// rebinds the workspace on its next call.
		remote.SetRelauncher(func() (*execd.Client, func(), error) {
			return execd.Launch(context.WithoutCancel(ctx))
		})
		remote.SetModeFunc(func(ctx context.Context) bool {
			// The child picks its unconfined runner from this flag,
			// so an approved escalation must travel with it: the
			// retry is a fresh start request on the same child.
			return IsYOLO(ctx, store) || escalated(ctx)
		})
		backend = remote
	} else {
		// One construction site for every platform: backend.go owns
		// which backend this is and how it is built.
		backend, err = hostBackend(s.Root, s.WritablePaths)
		if err != nil {
			return nil, err
		}
	}

	// Workspace mode chain: approval gate → env policy → OS backend.
	confined := coresandbox.WithApproval(
		coresandbox.WithDefaults(backend, coresandbox.ExecOptions{
			Env: s.Env(),
			Net: execNet,
		}),
		approver.Approve, approver.Allowlist())

	// YOLO chain: direct host execution with the full environment. In
	// remote mode this shares the execd child (the Unconfined flag is
	// set per request); locally it is a plain host runner.
	var unconfined coresandbox.Runner
	if s.Remote {
		unconfined = escalatingBridge{backend}
	} else {
		unconfined = sandboxlocal.New(s.Root)
	}
	return &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
		escalator:  escalator,
		rules:      rules,
		procs:      feed,
	}, nil
}
