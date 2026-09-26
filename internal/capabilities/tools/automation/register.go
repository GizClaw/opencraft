package automation

import (
	"context"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the opencraft.automations resource. A nil host yields
// an empty host so CLI/headless runtimes can still resolve the deploy
// graph; the tool source then exposes no tools.
type Factory struct {
	Host Host
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(_ context.Context, _ resource.Input) (any, error) {
	if f.Host == nil {
		return emptyHost{}, nil
	}
	return f.Host, nil
}
