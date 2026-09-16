package execd

import (
	goruntime "runtime"

	"github.com/GizClaw/opencraft/internal/foundation/utils/shelldetect"
)

// defaultShell is the shell reported by EnvironmentInfo. It resolves
// through the same detection the exec tools spawn through, so this
// informational field cannot name a shell the host does not actually
// use (Windows prefers PowerShell; /bin/sh stays the POSIX answer).
func defaultShell() string {
	return shelldetect.Detect(goruntime.GOOS).Program
}
