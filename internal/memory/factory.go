package memory

import (
	"context"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/opencraft/internal/memory/summary"
	"github.com/GizClaw/opencraft/internal/state"
)

// ResourceKind is the deploy resource kind for opencraft memory.
const ResourceKind = "memory"

// Factory builds the summary memory assembly from deploy settings,
// depending on a state resource of kind "state".
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec declares the resource shape: kind memory, impl summary,
// one required dependency on a state store.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "summary",
		Deps: []resource.DepSpec{
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
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	dep, ok := in.Dep("state")
	if !ok {
		return nil, errdefs.Validationf("memory: state dependency is required")
	}
	store, ok := dep.(*state.Store)
	if !ok {
		return nil, errdefs.Validationf("memory: state dep is not *state.Store")
	}
	policy, err := resource.DecodeTyped[policySettings](in.Settings)
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
