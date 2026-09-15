package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// petWindowHandle exposes the pet window's native handle to the pet
// package's cursor reader (an HWND on Windows, ignored elsewhere). 0
// means the window is gone.
func petWindowHandle(win *application.WebviewWindow) uintptr {
	if win == nil {
		return 0
	}
	return uintptr(win.NativeWindow())
}
