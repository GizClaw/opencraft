package sandbox

import (
	"errors"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Register adds opencraft's sandbox resource factories: the artifact
// sink, the process feed, the shared observing workspace, the mode-aware
// HostSandbox runner, and the HostWorkspace — to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		registerNetPolicy(r),
		resource.Register(r, ArtifactObserverFactory{}),
		resource.Register(r, ProcessFeedFactory{}),
		resource.Register(r, ObservingWorkspaceFactory{}),
		resource.Register(r, HostSandboxFactory{}),
		resource.Register(r, HostWorkspaceFactory{}),
	)
}
