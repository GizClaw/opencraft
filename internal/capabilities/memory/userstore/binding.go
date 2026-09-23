package userstore

import (
	"context"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// StoreContract is the deploy contract of the caller-owned user-memory
// store the host attaches to user.db. It is an external dependency
// (runtime.external_deps), so the store survives in-place runtime
// reloads instead of being rebuilt per generation.
const StoreContract = "opencraft.user_memory_store"

// ResourceKind is the deploy resource kind of the user-level memory
// binding: the store plus the document's settings, so a consumer that
// needs either one reads a single dependency.
const ResourceKind = "opencraft.user_memory"

// ResourceImpl is the deploy impl id of the binding.
const ResourceImpl = "local"

// Binding pairs the host's memory store with the validated settings.
type Binding struct {
	Memory Memory
	Config config.UserMemoryConfig
}

// Enabled reports whether the feature should do anything in this
// runtime: a user database is open and the settings page has not
// switched it off.
func (b *Binding) Enabled() bool {
	return b != nil && b.Memory != nil && !b.Memory.Empty() && b.Config.Enabled
}

// Settings is an alias of the deploy settings shape: the page and the
// runtime share one definition (foundation/config owns it).
type Settings = config.UserMemorySettings

// Factory builds the opencraft.user_memory resource from the injected
// store. A runtime without a user database injects the empty memory, so
// the deploy graph still resolves and consumers degrade instead of
// failing.
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
func (f Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	resolved, err := settings.Resolve()
	if err != nil {
		return nil, err
	}
	memory, err := resourcedep.Required[Memory](in, "user memory", "store")
	if err != nil {
		return nil, err
	}
	return &Binding{Memory: memory, Config: resolved}, nil
}
