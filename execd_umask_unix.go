//go:build !windows

package main

import "syscall"

// execdSocketUmask sets a user-only process umask around the execd
// unix-socket bind and returns a function that restores the previous
// umask. Creating the socket with mode 0777 under this umask yields
// 0700, so the file is never world-visible while it exists.
func execdSocketUmask() func() {
	prev := syscall.Umask(0o077)
	return func() { syscall.Umask(prev) }
}
