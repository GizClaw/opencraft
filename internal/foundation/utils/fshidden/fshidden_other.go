//go:build !windows

package fshidden

import (
	"io/fs"
	"strings"
)

// Hidden reports whether entry is hidden on this platform: a leading
// dot, the Unix convention.
func Hidden(entry fs.DirEntry) bool {
	return strings.HasPrefix(entry.Name(), ".")
}
