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
func (f Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft agentlifecycle: decode settings: %v", err)
	}
	if settings.Dir == "" {
		return nil, errdefs.Validationf(
			"opencraft agentlifecycle: settings.dir is required " +
				"(the Host injects the agents directory)")
	}
	if settings.WorkDir == "" || settings.UserDir == "" {
		return nil, errdefs.Validationf(
			"opencraft agentlifecycle: settings.work_dir and settings.user_dir " +
				"are required (subagent prepare-hook assembly values; the " +
				"deploy document expands ${ocraft:...} into them)")
	}
	return New(settings.Dir, settings.WorkDir, settings.UserDir)
}
