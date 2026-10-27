package memory

import (
	"context"
	"errors"

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

var _ resource.Factory[*summary.Assembly] = Factory{}

var (
	depMemSessions = resource.Require[*sessions.Store]("sessions")
	depMemRouter   = resource.Optional[*route.Router]("router")
	depMemObserver = resource.Optional[UsageObserver]("observer")
)

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
	if err := resource.Register(r, usageObserverFactory{
		fn: fn,
	}); err != nil {
		return err
	}
	return errors.Join(
		resource.Register(r, Factory{}),
		resource.Register(r, commitHookFactory{}),
		resource.Register(r, archiveObserverFactory{}),
	)
}

// Spec declares the resource shape: kind memory, impl summary,
// one required dependency on a state store.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "summary",
		Deps: []resource.Requirement{
			depMemSessions.Spec(),
			// Optional: the inference router. LLM condensation goes
			// through it so the compaction model follows the
			// user-editable routing policy; absent deployments stay
			// buffer-fold only.
			depMemRouter.Spec(), depMemObserver.Spec(),
		},
	}
}

type policySettings struct {
	MaxRawMessages  int `json:"max_raw_messages,omitempty"`
	PreserveRecent  int `json:"preserve_recent,omitempty"`
	MaxSummaryBytes int `json:"max_summary_bytes,omitempty"`
	// ReplayFullHistory is a pointer so a settings document that omits
	// the switch gets the shipped default instead of Go's zero value.
	ReplayFullHistory *bool `json:"replay_full_history,omitempty"`
}

// replayFullHistory is the policy's switch, on unless a deployment says
// otherwise. The default lives here and not only in the embedded deploy
// document: a custom deployment, a test fixture and a user layer that
// predates the switch all have to land on the same behaviour.
func (p policySettings) replayFullHistory() bool {
	if p.ReplayFullHistory == nil {
		return true
	}
	return *p.ReplayFullHistory
}

// New builds the summary Assembly over the SQLite adapter.
func (Factory) New(ctx context.Context, in resource.Input) (*summary.Assembly, error) {
	sessionsStore, err := depMemSessions.Get(in)
	if err != nil {
		return nil, err
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
		summary.WithReplayFullHistory(policy.replayFullHistory()),
	}
	if router, ok, err := depMemRouter.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		opts = append(opts, summary.WithRouter(router))
	}
	if observer, ok, err := depMemObserver.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		opts = append(opts, summary.WithUsageObserver(
			func(ctx context.Context, usage inference.Usage) {
				observer.ReportUsage(ctx, usage)
			},
		))
	}
	adapter := &sqliteTurnStore{db: sessionsStore.Database()}
	return summary.NewAssembly(adapter, opts...), nil
}
