package memory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// ResourceKind is the deploy resource kind for opencraft memory.
const ResourceKind = "memory"

// Factory builds the summary memory assembly from deploy settings,
// depending on the session store (which owns the SQLite state).
type Factory struct{}

// memoryResource exposes the summary assembly plus its SQLite turn
// store so lifecycle hooks can append memory rows inside the same
// transaction as the conversation archive.
type memoryResource struct {
	*summary.Assembly
	store *sqliteTurnStore
}

// AppendMessagesTx appends memory rows inside the caller's transaction.
func (r *memoryResource) AppendMessagesTx(
	ctx context.Context,
	tx *sql.Tx,
	conversationID, turnID string,
	msgs []message.Message,
) error {
	return r.store.AppendMessagesTx(ctx, tx, conversationID, turnID, msgs)
}

var _ resource.Factory = Factory{}

// RegisterWithObserver registers the memory resources plus an optional
// usage-observer resource supplied by the engine.
func RegisterWithObserver(
	r *resource.Registry,
	observe func(context.Context, inference.Usage),
) error {
	if observe != nil {
		if err := r.Register(usageObserverFactory{
			fn: UsageObserverFunc(observe),
		}); err != nil {
			return err
		}
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
	return &memoryResource{
		Assembly: summary.NewAssembly(adapter, opts...),
		store:    adapter,
	}, nil
}

func timeNow() time.Time { return time.Now().UTC() }
