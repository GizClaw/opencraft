package subagents

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// PolicyFactory builds the opencraft.delegation.policy resource.
type PolicyFactory struct{}

var _ resource.Factory = PolicyFactory{}

// Spec implements resource.Factory.
func (PolicyFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: PolicyResourceKind,
		Impl: "local",
	}
}

// New implements resource.Factory. A malformed pattern is a settings
// error rather than a silently ignored entry: a policy that does not
// do what it says is worse than no policy.
func (PolicyFactory) New(
	ctx context.Context, in resource.Input,
) (any, error) {
	settings, err := resource.DecodeTyped[PolicySettings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"delegation policy resource: decode settings: %v", err)
	}
	if err := config.ValidateTargetPatterns(
		settings.AllowedTargets, settings.BlockedTargets,
	); err != nil {
		return nil, errdefs.Validationf(
			"delegation policy resource: %v", err)
	}
	return NewPolicy(settings.AllowedTargets, settings.BlockedTargets), nil
}
