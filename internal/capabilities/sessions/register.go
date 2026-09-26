package sessions

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the session store resource. StoreFor lets a host
// share one Store across runtimes; nil opens a private store.
type Factory struct {
	StoreFor func(
		ctx context.Context, root string, window int,
	) (*Store, error)
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "opencraft"}
}

type settings struct {
	Root   string `json:"root"`
	Window int    `json:"window,omitempty"`
}

// New implements resource.Factory.
func (f Factory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[settings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf("session store: %v", err)
	}
	if f.StoreFor == nil {
		return nil, errdefs.NotAvailablef(
			"session store: StoreFor is required; schema migration is " +
				"centralized in internal/foundation/compat")
	}
	return f.StoreFor(ctx, s.Root, s.Window)
}
