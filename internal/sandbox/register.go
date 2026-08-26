package sandbox

import (
	"errors"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Register adds opencraft's sandbox resource factories — the
// mode-aware HostSandbox runner and the HostWorkspace — to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		registerNetPolicy(r),
		r.Register(HostSandboxFactory{}),
		r.Register(HostWorkspaceFactory{}),
	)
}
