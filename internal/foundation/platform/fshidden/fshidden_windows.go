//go:build windows

package fshidden

import (
	"io/fs"
	"strings"
	"syscall"
)

// Hidden reports whether entry is hidden on Windows: a leading dot (the
// convention developer tooling uses) or the attributes Explorer hides,
// FILE_ATTRIBUTE_HIDDEN and FILE_ATTRIBUTE_SYSTEM.
func Hidden(entry fs.DirEntry) bool {
	if strings.HasPrefix(entry.Name(), ".") {
		return true
	}
	info, err := entry.Info()
	if err != nil {
		// Attributes unreadable: treat the entry as visible rather than
		// swallowing something the user can still open.
		return false
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	const hiddenOrSystem = syscall.FILE_ATTRIBUTE_HIDDEN |
		syscall.FILE_ATTRIBUTE_SYSTEM
	return data.FileAttributes&hiddenOrSystem != 0
}
