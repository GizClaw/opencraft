//go:build !darwin && !windows && !linux

package pet

// cursorPosition is not implemented on platforms the desktop shell does
// not target.
func cursorPosition(_ uintptr) (x, y int, ok bool) {
	return 0, 0, false
}
