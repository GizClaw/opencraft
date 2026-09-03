// Package sandbox implements opencraft's per-session sandbox: a
// resource-ized host runner (HostSandbox) that switches between the
// confined chain (approval gate → env policy → seatbelt/bwrap/execd)
// and direct host execution (YOLO) based on the session's persisted
// permission mode, plus the workspace counterpart (HostWorkspace) and
// the execd child runner construction.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"
	sbwindows "github.com/GizClaw/flowcraft/core/sandbox/windows"
	corenet "github.com/GizClaw/flowcraft/core/utils/net"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

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
	if h.Unconfined(ctx) {
		return h.unconfined.Start(ctx, spec)
	}
	// Read-only mode narrows the per-call write policy before the
	// approval gate and the OS backend see it: the runner root is
	// dropped from the writable set for this command (explicit
	// writable paths like the cache stay writable). The approver sees
	// the same Opts, so it can auto-allow known read-only commands.
	if isReadOnly(ctx, h.sessions) {
		spec.Opts.Write = coresandbox.WriteReadOnly
	}
	return h.confined.Start(ctx, spec)
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
	if store == nil {
		return false
	}
	sessionID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		sessionID = info.ConversationID
	}
	if sessionID == "" {
		return false
	}
	mode, err := store.Mode(ctx, sessionID)
	return err == nil && mode.IsYOLO()
}

// isReadOnly resolves the current session's read-only flag from the
// execution context, mirroring IsYOLO. Sessions without a persisted
// mode run workspace-write and return false.
func isReadOnly(ctx context.Context, store *sessions.Store) bool {
	if store == nil {
		return false
	}
	sessionID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		sessionID = info.ConversationID
	}
	if sessionID == "" {
		return false
	}
	mode, err := store.Mode(ctx, sessionID)
	return err == nil && mode.IsReadOnly()
}

// noopCloseRunner delegates everything but Close, which is owned by
// the confined chain (both paths share one remote backend).
type noopCloseRunner struct {
	coresandbox.Runner
}

func (noopCloseRunner) Close() error { return nil }

// HostSandboxSettings is the deploy-document shape of the sandbox.Runner
// resource (impl opencraft): the workspace root, sandbox backends, and
// the environment policy applied in workspace mode.
type HostSandboxSettings struct {
	Root          string           `json:"root"`
	WritablePaths []string         `json:"writable_paths,omitempty"`
	Remote        bool             `json:"remote,omitempty"`
	EnvPolicy     *EnvPolicyConfig `json:"env_policy,omitempty"`
}

// Env converts the configured environment policy into the sandbox
// EnvPolicy applied to every confined spawn.
func (s HostSandboxSettings) Env() coresandbox.EnvPolicy {
	if s.EnvPolicy == nil {
		// No explicit policy: sandboxed commands get the curated
		// allowlist instead of the parent environment wholesale, so
		// provider keys never leak into project code by default.
		return DefaultEnvPolicy()
	}
	return coresandbox.EnvPolicy{
		Allow:  s.EnvPolicy.Allow,
		Inject: s.EnvPolicy.Inject,
	}
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
	store, err := resourcedep.Required[*sessions.Store](
		in, "opencraft sandbox", "sessions")
	if err != nil {
		return nil, err
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
		polJSON, err := json.Marshal(s.SandboxPolicy())
		if err != nil {
			return nil, errdefs.Validationf(
				"opencraft sandbox: encode env policy: %v", err)
		}
		client, _, stop, err := execd.Launch(ctx, s.Root, string(polJSON))
		if err != nil {
			return nil, err
		}
		remote, err := execd.NewRemoteRunner(ctx, client, stop)
		if err != nil {
			return nil, err
		}
		remote.SetModeFunc(func(ctx context.Context) bool {
			return IsYOLO(ctx, store)
		})
		backend = remote
	} else {
		switch goruntime.GOOS {
		case "darwin":
			backend, err = seatbelt.New(s.Root,
				seatbelt.WithWritablePaths(s.WritablePaths...))
		case "linux":
			backend, err = bwrap.New(s.Root,
				bwrap.WithWritablePaths(s.WritablePaths...))
		case "windows":
			// flowcraft v0.2.2 Windows backend with OS-level write
			// confinement; interactive sessions are disabled on
			// Windows (issue #38) and the capability surface is kept
			// honest.
			var wb coresandbox.Runner
			if wb, err = sbwindows.New(s.Root,
				sbwindows.WithWriteConfinement(),
				sbwindows.WithWritablePaths(s.WritablePaths...)); err == nil {
				backend = noTTYRunner{wb}
			}
		default:
			backend = sandboxlocal.New(s.Root)
		}
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
		unconfined = noopCloseRunner{backend}
	} else {
		unconfined = sandboxlocal.New(s.Root)
	}
	return &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
	}, nil
}
