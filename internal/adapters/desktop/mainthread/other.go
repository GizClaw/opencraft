//go:build !darwin

package mainthread

// Run runs fn directly on Windows and Linux: the systray integration
// starts its own loops and does not need the native main thread.
func Run(fn func()) {
	fn()
}
