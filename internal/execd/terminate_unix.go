//go:build !windows

package execd

import (
	"os/exec"
	"syscall"
)

// terminateExecd asks the execd child to shut down gracefully: SIGTERM
// runs the child's own cleanup (terminating sandbox sessions) before
// stop's timed SIGKILL fallback.
func terminateExecd(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
