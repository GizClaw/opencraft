package memory

import (
	"context"
	"time"

	"github.com/GizClaw/flowcraft/sdk/config"
	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/opencraft/internal/memory/summary"
	"github.com/GizClaw/opencraft/internal/state"
)

// ResourceKind is the deploy resource kind for opencraft memory.
const ResourceKind = "memory"

// Factory builds the summary memory assembly from deploy settings,
// depending on a state resource of kind "state".
type Factory struct{}

var _ config.Factory = Factory{}

// Spec declares the resource shape: kind memory, impl summary,
// one required dependency on a state store.
func (Factory) Spec() config.Spec {
	return config.Spec{
		Kind: ResourceKind,
		Impl: "summary",
		Deps: []config.DepSpec{
			{Name: "state", Type: state.ResourceKind, Required: true},
		},
	}
}

type policySettings struct {
	MaxRawMessages  int `json:"max_raw_messages,omitempty"`
	PreserveRecent  int `json:"preserve_recent,omitempty"`
	MaxSummaryBytes int `json:"max_summary_bytes,omitempty"`
}

// New builds the summary Assembly over the SQLite adapter.
func (Factory) New(ctx context.Context, in config.Input) (any, error) {
	dep, ok := in.Dep("state")
	if !ok {
		return nil, errdefs.Validationf("memory: state dependency is required")
	}
	store, ok := dep.(*state.Store)
	if !ok {
		return nil, errdefs.Validationf("memory: state dep is not *state.Store")
	}
	policy, err := config.DecodeSettings[policySettings](in.Settings)
	if err != nil {
		return nil, err
	}
	opts := []summary.AssemblyOption{
		summary.WithAssemblyPolicy(summary.Policy{
			MaxRawMessages:  policy.MaxRawMessages,
			PreserveRecent:  policy.PreserveRecent,
			MaxSummaryBytes: policy.MaxSummaryBytes,
		}),
	}
	return summary.NewAssembly(&sqliteTurnStore{s: store}, opts...), nil
}

func timeNow() time.Time { return time.Now().UTC() }
