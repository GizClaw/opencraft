package execserver

import "github.com/GizClaw/flowcraft/sdk/sandbox"

// Backend is the upstream primitive surface the exec-server wraps:
// process lifecycle plus the optional signal and event-push capabilities.
type Backend interface {
	sandbox.ProcessManager
}

// BackendOf adapts a runner to Backend when it implements ProcessManager.
func BackendOf(pm sandbox.ProcessManager) Backend { return pm }
