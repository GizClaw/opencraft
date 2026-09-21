package db

import (
	"path/filepath"
	"testing"
)

// TestOpenAppliesSharedPragmas pins the connection settings the stores
// rely on: WAL for concurrency, NORMAL so a WAL commit costs one fsync
// instead of two, and a busy timeout so a momentary writer collision
// waits instead of failing. The write path of every turn depends on
// these, so a change here has to be deliberate.
func TestOpenAppliesSharedPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.db")
	handle, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	read := func(pragma string) string {
		t.Helper()
		var value string
		if err := handle.SQLDB().QueryRow(pragma).Scan(&value); err != nil {
			t.Fatalf("%s: %v", pragma, err)
		}
		return value
	}
	if got := read("PRAGMA journal_mode"); got != "wal" {
		t.Fatalf("journal_mode = %q, want wal", got)
	}
	if got := read("PRAGMA synchronous"); got != "1" {
		t.Fatalf("synchronous = %q, want 1 (NORMAL)", got)
	}
	if got := read("PRAGMA busy_timeout"); got != "5000" {
		t.Fatalf("busy_timeout = %q, want 5000", got)
	}
	if got := read("PRAGMA foreign_keys"); got != "1" {
		t.Fatalf("foreign_keys = %q, want 1", got)
	}
}
