package pet

// CursorPosition returns the global pointer position in the same DIP
// space window coordinates use, or ok=false on a platform that cannot
// report it.
//
// A desktop pet cannot take hover from its webview: mouse-move events
// only reach a webview while its window is the key window of the active
// application, and an always-on-top companion never is. Reading the
// pointer from the platform works while the user is in another app,
// which is exactly when a pet gets hovered.
//
// handle is the platform window handle (HWND on Windows) used to resolve
// the monitor's DPI. Platforms whose cursor API already reports points
// ignore it; 0 means "no window yet".
func CursorPosition(handle uintptr) (x, y int, ok bool) {
	return cursorPosition(handle)
}
