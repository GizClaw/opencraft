package store

import (
	"context"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// StoreContract is the deploy contract of the caller-owned review queue
// the host attaches to user.db. It is an external dependency, so the
// queue survives in-place runtime reloads.
const StoreContract = "opencraft.review_queue_store"

// ResourceKind is the deploy resource kind of the review binding: the
// queue plus the document's trigger settings.
const ResourceKind = "opencraft.review"

// ResourceImpl is the deploy impl id of the binding.
const ResourceImpl = "local"

// Binding pairs the host's suggestion queue with the validated review
// settings the observe hook triggers on.
type Binding struct {
	Queue  Queue
	Config config.ReviewConfig
}

// Enabled reports whether a review may run in this runtime: a user
// database is open and the settings page switched the review on. The
// shipped default is off.
func (b *Binding) Enabled() bool {
	return b != nil && b.Queue != nil && !b.Queue.Empty() && b.Config.Enabled
}

// Settings is an alias of the deploy settings shape: the page and the
// runtime share one definition (foundation/config owns it).
type Settings = config.ReviewSettings

// Factory builds the opencraft.review_queue resource from the injected
// queue. A runtime without a user database injects the empty queue,
// which accepts no suggestion.
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
	queue, err := resourcedep.Required[Queue](in, "review queue", "store")
	if err != nil {
		return nil, err
	}
	return &Binding{Queue: queue, Config: resolved}, nil
}
