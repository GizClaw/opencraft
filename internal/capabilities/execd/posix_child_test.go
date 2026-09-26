package execd

import (
	"runtime"
	"testing"
)

// requirePOSIXChild skips a case whose scenario needs POSIX child
// semantics: its child argv is a POSIX shell (/bin/sh -c, /bin/cat,
// /bin/sleep), it interrupts or kills a process group, or it leans on
// POSIX signal delivery. Windows has none of those, and the Windows
// lane has to show the gap as a skip instead of a pass — the number of
// guarding calls is written into .github/workflows/ci.yml.
//
// What runs on Windows is everything that never spawns a shell: the
// wire protocol, the frame writer, the orphan journal's stale-record
// sweep, the pool's bookkeeping over in-process children, and the real
// child that LaunchExe starts over the named-pipe channel. A guard here
// is deliberately explicit rather than a `-short` switch, so a renamed
// or newly added case fails the lane loudly instead of being silently
// excluded from it.
func requirePOSIXChild(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell/signal child: " +
			"the Windows lane runs the shell-free cases")
	}
}
