package secrets

import (
	"context"
	"fmt"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/secret"
)

// factory builds the secret.Store/keychain resource.
type factory struct{}

// Spec implements resource.Factory.
func (factory) Spec() resource.Spec {
	return resource.Spec{Kind: secret.ResourceKind, Impl: ResourceImpl}
}

// New implements resource.Factory.
func (factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, fmt.Errorf("opencraft secrets: decode settings: %w", err)
	}
	store, err := NewStore(settings.Dir)
	if err != nil {
		return nil, err
	}
	store.id = settings.ID
	store.def = settings.Default
	return store, nil
}

// Register adds the secret.Store/keychain factory to r.
func Register(r *resource.Registry) error {
	return r.Register(factory{})
}
