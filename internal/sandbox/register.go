package sandbox

import (
	"errors"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Register adds opencraft's sandbox resource factories: the artifact
// sink, the shared observing workspace, the mode-aware HostSandbox
// runner, and the HostWorkspace — to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		registerNetPolicy(r),
		r.Register(ArtifactObserverFactory{}),
		r.Register(ObservingWorkspaceFactory{}),
		r.Register(HostSandboxFactory{}),
		r.Register(HostWorkspaceFactory{}),
	)
}
