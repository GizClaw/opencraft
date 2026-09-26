package plugininstall

import (
	"context"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the opencraft.plugin_installer resource. A nil
// installer yields the empty one, so runtimes without a desktop
// registry still resolve the deploy graph.
type Factory struct {
	Installer Installer
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(
	_ context.Context, _ resource.Input,
) (any, error) {
	if f.Installer == nil {
		return EmptyInstaller(), nil
	}
	return f.Installer, nil
}
