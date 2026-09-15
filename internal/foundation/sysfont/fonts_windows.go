//go:build windows

package sysfont

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// defaultCharset asks GDI to enumerate every character set available on
	// the device, not just the ANSI one.
	defaultCharset = 1
	// hwndDesktop is the HWND_DESKTOP pseudo handle GetDC accepts.
	hwndDesktop = 0
)

var (
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procEnumFontFamiliesExW = gdi32.NewProc("EnumFontFamiliesExW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
)

// logFontW mirrors LOGFONTW. Only the character set is filled in, but GDI
// reads the whole structure, so the layout has to match exactly.
type logFontW struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	FaceName       [32]uint16
}

// enumLogFontExW mirrors ENUMLOGFONTEXW, the structure GDI hands the
// EnumFontFamiliesEx callback.
type enumLogFontExW struct {
	LogFont  logFontW
	FullName [64]uint16
	Style    [32]uint16
	Script   [32]uint16
}

// listSystemFonts enumerates the GDI font families. One family shows up once
// per character set it covers; the catalogue filter folds those back together.
func listSystemFonts() ([]string, error) {
	hdc, _, _ := procGetDC.Call(hwndDesktop)
	if hdc == 0 {
		return nil, errors.New("sysfont: GetDC failed")
	}
	defer procReleaseDC.Call(hwndDesktop, hdc)

	var families []string
	callback := syscall.NewCallback(func(
		logFont *enumLogFontExW,
		_ uintptr,
		_ uint32,
		_ uintptr,
	) uintptr {
		if name := windows.UTF16ToString(logFont.LogFont.FaceName[:]); name != "" {
			families = append(families, name)
		}
		return 1 // keep enumerating
	})

	query := logFontW{CharSet: defaultCharset}
	ret, _, callErr := procEnumFontFamiliesExW.Call(
		hdc,
		uintptr(unsafe.Pointer(&query)),
		callback,
		0,
		0,
	)
	if ret == 0 {
		return nil, fmt.Errorf("sysfont: EnumFontFamiliesExW failed: %w", callErr)
	}
	return families, nil
}
