//go:build !windows

package fshidden

import "testing"

func TestHiddenFollowsTheDotConvention(t *testing.T) {
	dir := t.TempDir()
	writeEntries(t, dir, ".env", "notes.txt")
	entries := entriesByName(t, dir)

	if !Hidden(entries[".env"]) {
		t.Error(".env must be hidden off Windows")
	}
	if Hidden(entries["notes.txt"]) {
		t.Error("notes.txt must stay visible")
	}
}
