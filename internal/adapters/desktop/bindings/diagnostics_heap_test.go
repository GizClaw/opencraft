package bindings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWriteHeapProfile pins the diagnostics capture path: the profile
// lands under the diagnostics directory with a timestamped name and a
// non-empty gzip-compressed payload that `go tool pprof` can read.
func TestWriteHeapProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	at := time.Date(2026, 9, 20, 12, 34, 56, 0, time.UTC)
	result, err := writeHeapProfile(dir, at)
	if err != nil {
		t.Fatalf("writeHeapProfile: %v", err)
	}
	if want := filepath.Join(dir, "heap-20260920-123456.pprof"); result.Path != want {
		t.Fatalf("path = %q, want %q", result.Path, want)
	}
	if result.Bytes <= 0 {
		t.Fatalf("profile is empty")
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	// pprof stream profiles are gzip-compressed; the magic bytes are what
	// `go tool pprof` keys on.
	if !strings.HasPrefix(string(raw[:2]), "\x1f\x8b") {
		t.Fatalf("profile is not gzip-compressed: % x", raw[:2])
	}
}
