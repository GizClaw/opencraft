package engine

import (
	"context"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/resource"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// delegationSessionsDep carries the workspace session store into the
// delegation service, so the app-owned session initializer has the row
// it writes the subagent's permission mode into.
const delegationSessionsDep = "sessions"

// delegationServiceFactory registers flowcraft's local delegation service
// with opencraft's session initializer attached.
//
// The store is a deploy dependency, so it is only known while the
// generation is being built; core's factory fixes its options at
// registration time, before any dependency resolves. This wrapper
// therefore builds a fresh core factory per generation, with that
// generation's store captured in the option (see
// [ocsessions.ModeInitializer]) instead of sharing one across reloads.
type delegationServiceFactory struct {
	opts []sdkdelegation.Option
}

var _ resource.Factory[*sdkdelegation.LocalService] = delegationServiceFactory{}

// depDelegationSessions is the session store the delegated runs are
// initialized against.
var depDelegationSessions = resource.Require[*ocsessions.Store](delegationSessionsDep)

// Spec implements resource.Factory: the core service's deps plus the
// session store.
func (f delegationServiceFactory) Spec() resource.Spec {
	spec := sdkdelegation.NewServiceFactory().Spec()
	spec.Deps = append(spec.Deps, depDelegationSessions.Spec())
	return spec
}

// New implements resource.Factory.
func (f delegationServiceFactory) New(
	ctx context.Context, in resource.Input,
) (*sdkdelegation.LocalService, error) {
	store, err := depDelegationSessions.Get(in)
	if err != nil {
		return nil, err
	}
	opts := make([]sdkdelegation.Option, 0, len(f.opts)+1)
	opts = append(opts, f.opts...)
	// A nil initializer is an option error in core, not a no-op; the
	// store is a required dependency, but a typed-nil one would land
	// here and there is nothing to install for it.
	if initializer := ocsessions.ModeInitializer(store); initializer != nil {
		opts = append(opts,
			sdkdelegation.WithSessionInitializer(initializer))
	}
	return sdkdelegation.NewServiceFactory(opts...).New(ctx, in)
}
