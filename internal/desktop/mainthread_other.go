//go:build !darwin

package desktop

// runOnMain runs fn directly on Windows and Linux: the systray
// integration starts its own goroutine loops (Windows message pump,
// Linux D-Bus) and neither needs the application main thread.
func runOnMain(fn func()) {
	fn()
}
