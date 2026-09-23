package memory

import (
	"context"
	"errors"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// ResourceKind is the deploy resource kind for opencraft memory.
const ResourceKind = "memory"

// Factory builds the summary memory assembly from deploy settings,
// depending on the session store (which owns the SQLite state).
type Factory struct{}

var _ resource.Factory = Factory{}

// RegisterWithObserver registers the memory resources plus the usage
// observer the engine supplies. The observer resource is registered
// unconditionally (a no-op when the engine has none): the deploy
// document declares it once, so every optional `observer` dependency
// resolves whether or not this runtime accounts for usage.
func RegisterWithObserver(
	r *resource.Registry,
	observe func(context.Context, inference.Usage),
) error {
	fn := UsageObserverFunc(observe)
	if observe == nil {
		fn = func(context.Context, inference.Usage) {}
	}
	if err := r.Register(usageObserverFactory{
		fn: fn,
	}); err != nil {
		return err
	}
	return errors.Join(
		r.Register(Factory{}),
		r.Register(commitHookFactory{}),
		r.Register(archiveObserverFactory{}),
	)
}

// Spec declares the resource shape: kind memory, impl summary,
// one required dependency on a state store.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "summary",
		Deps: []resource.DepSpec{
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
			// Optional: the inference router. LLM condensation goes
			// through it so the compaction model follows the
			// user-editable routing policy; absent deployments stay
			// buffer-fold only.
			{Name: "router", Type: "inference.Router", Required: false},
			{Name: "observer", Type: UsageObserverResourceKind, Required: false},
		},
	}
}

type policySettings struct {
	MaxRawMessages    int  `json:"max_raw_messages,omitempty"`
	PreserveRecent    int  `json:"preserve_recent,omitempty"`
	MaxSummaryBytes   int  `json:"max_summary_bytes,omitempty"`
	ReplayFullHistory bool `json:"replay_full_history,omitempty"`
}

// New builds the summary Assembly over the SQLite adapter.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	dep, ok := in.Dep("sessions")
	if !ok {
		return nil, errdefs.Validationf(
			"memory: sessions dependency is required")
	}
	sessionsStore, ok := dep.(*sessions.Store)
	if !ok {
		return nil, errdefs.Validationf(
			"memory: sessions dep is not *sessions.Store")
	}
	policy, err := resource.DecodeTyped[policySettings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	opts := []summary.AssemblyOption{
		summary.WithAssemblyPolicy(summary.Policy{
			MaxRawMessages:  policy.MaxRawMessages,
			PreserveRecent:  policy.PreserveRecent,
			MaxSummaryBytes: policy.MaxSummaryBytes,
		}),
		summary.WithReplayFullHistory(policy.ReplayFullHistory),
	}
	if dep, ok := in.Dep("router"); ok {
		if router, ok := dep.(*route.Router); ok {
			opts = append(opts, summary.WithRouter(router))
		}
	}
	if dep, ok := in.Dep("observer"); ok {
		if observer, ok := dep.(UsageObserver); ok {
			opts = append(opts, summary.WithUsageObserver(
				func(ctx context.Context, usage inference.Usage) {
					observer.ReportUsage(ctx, usage)
				},
			))
		}
	}
	adapter := &sqliteTurnStore{db: sessionsStore.Database()}
	return summary.NewAssembly(adapter, opts...), nil
}
