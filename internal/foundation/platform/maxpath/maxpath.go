// Package maxpath answers one question about a host OS: how long a path
// its file APIs accept before they refuse it.
//
// It exists because the refusals differ in kind. Windows with long-path
// support off takes MAX_PATH (260) characters with the terminating NUL
// included — 259 characters of path — and refuses a longer one with a
// not-found-ish error that names neither the path nor its length. A
// caller that copies a package into a deep directory therefore fails
// partway through, files already written, with an error nobody can act
// on; the number has to be known before the copy starts. POSIX caps a
// single *name* at 255 bytes instead, and that failure reads as itself
// ("file name too long"), so there is nothing to say up front there:
// Limit answers 0, which callers read as "no cap you have to check for".
//
// The number is the fact; the decision to refuse a package whose files
// would land past it belongs to the caller (capabilities/apps).
package maxpath

import "runtime"

// windowsMaxPath is MAX_PATH (260) less the terminating NUL the Windows
// file APIs need.
const windowsMaxPath = 259

// Limit returns the longest path the file APIs of goos accept, or 0 when
// goos puts no cap on a whole path that a user cannot read about.
func Limit(goos string) int {
	if goos == "windows" {
		return windowsMaxPath
	}
	return 0
}

// HostLimit is Limit for the OS this process runs on: the cap a copy or
// a write has to respect here.
func HostLimit() int {
	return Limit(runtime.GOOS)
}
