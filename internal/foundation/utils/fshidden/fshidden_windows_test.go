//go:build windows

package fshidden

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHiddenFollowsWindowsAttributes(t *testing.T) {
	dir := t.TempDir()
	writeEntries(t, dir, "bookmarks.txt", "notes.txt", ".env")
	hidden, err := windows.UTF16PtrFromString(
		filepath.Join(dir, "bookmarks.txt"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(
		hidden,
		windows.FILE_ATTRIBUTE_HIDDEN,
	); err != nil {
		t.Skipf("cannot set the hidden attribute here: %v", err)
	}
	entries := entriesByName(t, dir)

	if !Hidden(entries["bookmarks.txt"]) {
		t.Error("an attribute-hidden file must be hidden")
	}
	// Explorer shows dot-prefixed names, but .venv/.next are the
	// developer convention and must not be walked by default.
	if !Hidden(entries[".env"]) {
		t.Error(".env must be hidden on Windows too")
	}
	if Hidden(entries["notes.txt"]) {
		t.Error("notes.txt must stay visible")
	}
}
