//go:build windows

package main

// execdSocketUmask is a no-op on Windows: there is no process umask,
// and the socket file's security is enforced by the OS default ACLs
// on the user-local cache directory.
func execdSocketUmask() func() {
	return func() {}
}
