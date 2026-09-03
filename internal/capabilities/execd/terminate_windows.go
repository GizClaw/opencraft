//go:build windows

package execd

import "os/exec"

// terminateExecd is a no-op on Windows: SIGTERM is not deliverable
// there. The parent closes the client connection before this is called
// (see fork.go stop), so the child's Serve loop sees EOF and runs its
// own session cleanup; stop's timed Kill is the hard bound. Sandboxed
// processes live in job objects with KILL_ON_JOB_CLOSE, so even the
// hard kill cannot leak process trees.
func terminateExecd(*exec.Cmd) error { return nil }
