package agents

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the opencraft.agentlifecycle resource: the persistent
// subagent registry. Every generation's resource instance is bound to
// the same runtime registrar after Build, so reloads keep working
// without re-binding.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec declares the resource contract.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "local",
	}
}

// New creates the lifecycle rooted at settings.dir.
func (f Factory) New(_ context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft agentlifecycle: decode settings: %v", err)
	}
	if settings.Dir == "" {
		settings.Dir, err = DefaultAgentsDir()
		if err != nil {
			return nil, err
		}
	}
	return New(settings.Dir)
}
