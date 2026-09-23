package usage

import (
	"context"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// StoreContract is the deploy contract of the caller-owned skill
// lifecycle store the host attaches to user.db. It is an external
// dependency, so the store survives in-place runtime reloads.
const StoreContract = "opencraft.skill_usage_store"

// ResourceKind is the deploy resource kind of the skill lifecycle
// binding: the store plus the document's thresholds, so the registry,
// the curator and the skills page read one dependency.
const ResourceKind = "opencraft.skill_lifecycle"

// ResourceImpl is the deploy impl id of the binding.
const ResourceImpl = "local"

// Binding pairs the host's lifecycle store with the validated settings.
type Binding struct {
	Store  Lifecycle
	Config config.SkillLifecycleConfig
}

// Enabled reports whether usage should be recorded in this runtime: a
// user database is open and the settings page has not switched the
// lifecycle off. The thresholds stay readable when recording is off;
// there is simply no data behind them.
func (b *Binding) Enabled() bool {
	return b != nil && b.Store != nil && !b.Store.Empty() && b.Config.Enabled
}

// Settings is an alias of the deploy settings shape: the page and the
// runtime share one definition (foundation/config owns it).
type Settings = config.SkillLifecycleSettings

// Factory builds the opencraft.skill_usage resource from the injected
// store. A runtime without a user database injects the empty lifecycle,
// which records nothing and retires nothing.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "store", Type: StoreContract, Required: true},
		},
	}
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	resolved, err := settings.Resolve()
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[Lifecycle](in, "skill lifecycle", "store")
	if err != nil {
		return nil, err
	}
	return &Binding{Store: store, Config: resolved}, nil
}
