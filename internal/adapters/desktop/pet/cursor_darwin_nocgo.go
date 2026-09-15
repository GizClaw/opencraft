//go:build darwin && !cgo

package pet

// cursorPosition is unavailable without cgo, so hover stays inactive in
// such a build; clicks and drags are unaffected.
func cursorPosition(_ uintptr) (x, y int, ok bool) {
	return 0, 0, false
}
