package execd

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSweepStaleSockets pins what a crash leaves behind and what the
// sweep may take: only this package's sockets, and only ones old enough
// that no live child can own them.
func TestSweepStaleSockets(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(name string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := now.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write("execd-0d1f2e3a4b5c6d7e.sock", 48*time.Hour)
	fresh := write("execd-9988776655443322.sock", time.Hour)
	foreign := write("tmux.sock", 48*time.Hour)
	notSocket := write("execd-logs.txt", 48*time.Hour)
	subdir := filepath.Join(dir, "execd-nested.sock")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}

	if removed := sweepStaleSockets(dir, now); removed != 1 {
		t.Fatalf("removed %d entries, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale socket still present: %v", err)
	}
	for _, path := range []string{fresh, foreign, notSocket, subdir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("sweep removed %s: %v", filepath.Base(path), err)
		}
	}
}

// TestSweepStaleSocketsOnceUsesTheSocketDir pins the hook the launch
// path uses: it resolves the real socket directory, sweeps it once, and
// leaves a socket that is still young alone.
func TestSweepStaleSocketsOnceUsesTheSocketDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Other tests in this package launch real children, which consumes
	// the per-process guard. Reset it so this test observes the sweep.
	socketSweepOnce = sync.Once{}
	defer func() { socketSweepOnce = sync.Once{} }()

	dir, err := socketDir()
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	stale := filepath.Join(dir, "execd-1111222233334444.sock")
	fresh := filepath.Join(dir, "execd-5555666677778888.sock")
	now := time.Now()
	for path, age := range map[string]time.Duration{
		stale: 48 * time.Hour,
		fresh: time.Minute,
	} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := now.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	sweepStaleSocketsOnce(context.Background())

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("launch hook left the stale socket in place: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("launch hook removed a live socket: %v", err)
	}
}

// TestSweepStaleSocketsMissingDirIsSilent keeps the sweep best-effort:
// a directory that cannot be read is not a launch failure.
func TestSweepStaleSocketsMissingDirIsSilent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if removed := sweepStaleSockets(missing, time.Now()); removed != 0 {
		t.Fatalf("removed %d entries from a missing dir, want 0", removed)
	}
}
