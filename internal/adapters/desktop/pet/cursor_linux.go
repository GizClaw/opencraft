//go:build linux

package pet

// cursorPosition has no implementation on Linux yet: Wayland gives
// clients no way to read the global pointer at all (it would need the
// RemoteDesktop portal), and X11 would need XQueryPointer through
// libX11. Hover therefore stays inactive there rather than reporting a
// wrong position; clicks and drags are unaffected, and window placement
// is already limited on Wayland because clients cannot position
// toplevel windows.
func cursorPosition(_ uintptr) (x, y int, ok bool) {
	return 0, 0, false
}
