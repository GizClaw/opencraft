//go:build windows

package pet

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type ocPoint struct {
	X int32
	Y int32
}

var (
	ocUser32                  = windows.NewLazySystemDLL("user32.dll")
	ocGetCursorPos            = ocUser32.NewProc("GetCursorPos")
	ocGetDpiForWindow         = ocUser32.NewProc("GetDpiForWindow")
	ocGetDpiForSystem         = ocUser32.NewProc("GetDpiForSystem")
	ocDefaultDPI      uintptr = 96
)

// cursorPosition reads GetCursorPos, which reports physical pixels, and
// scales the result back to DIP by the DPI of the window's monitor.
func cursorPosition(handle uintptr) (x, y int, ok bool) {
	var point ocPoint
	result, _, _ := ocGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	if result == 0 {
		return 0, 0, false
	}
	x, y = int(point.X), int(point.Y)
	if dpi := ocWindowDPI(handle); dpi > int(ocDefaultDPI) {
		x = x * int(ocDefaultDPI) / dpi
		y = y * int(ocDefaultDPI) / dpi
	}
	return x, y, true
}

// ocWindowDPI resolves the window's DPI, falling back to the system
// value and then to 96 (100%); GetDpiForWindow needs Windows 10 1607,
// which is the floor the desktop shell already requires.
func ocWindowDPI(handle uintptr) int {
	if handle != 0 {
		if dpi, _, _ := ocGetDpiForWindow.Call(handle); dpi > 0 {
			return int(dpi)
		}
	}
	if dpi, _, _ := ocGetDpiForSystem.Call(); dpi > 0 {
		return int(dpi)
	}
	return int(ocDefaultDPI)
}
