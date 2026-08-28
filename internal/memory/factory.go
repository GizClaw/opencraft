package memory

import (
	"context"
	"errors"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/opencraft/internal/memory/summary"
	"github.com/GizClaw/opencraft/internal/sessions"
)

// ResourceKind is the deploy resource kind for opencraft memory.
const ResourceKind = "memory"

// Factory builds the summary memory assembly from deploy settings,
// depending on the session store (which owns the SQLite state).
type Factory struct{}

var _ resource.Factory = Factory{}

// Register adds the memory assembly factory plus the opencraft.commit
// (completed turns) and opencraft.archive (interrupted/failed turns)
// hook factories to r.
func Register(r *resource.Registry) error {
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
	return summary.NewAssembly(
		&sqliteTurnStore{s: sessionsStore.State()}, opts...), nil
}

func timeNow() time.Time { return time.Now().UTC() }
