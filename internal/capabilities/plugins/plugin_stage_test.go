package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stageDirs lists the staging directories of a registry root. They are
// dot-prefixed, so they are exactly what a registry scan must skip and
// what a leak after a failed install looks like.
func stageDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".plugin-stage-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestInstallStagesOutsideTheStoreLock pins what the staging directory
// buys: the copy of a plugin's files runs while the store lock is free,
// so a registry reader is answered during an install instead of after
// it. A FIFO in the source holds the copy open — copyDir reads every
// file, and opening a FIFO for reading waits for a writer.
func TestInstallStagesOutsideTheStoreLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a FIFO is not a portable way to hold a copy open")
	}
	root := t.TempDir()
	store := NewStore(root)

	src := t.TempDir()
	writePlugin(t, src, ".", map[string]any{
		"id": "slow", "name": "Slow", "version": "1.0.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "console.log('slow')")
	fifo := filepath.Join(src, "payload.txt")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	installed := make(chan error, 1)
	go func() {
		_, err := store.Install(src)
		installed <- err
	}()

	// The staging directory appears before the copy reaches the FIFO, so
	// its existence is the signal that an install is in flight.
	waitFor(t, "the install to start staging", func() bool {
		return len(stageDirs(t, root)) > 0
	})

	read := make(chan error, 1)
	go func() {
		_, err := store.List()
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("List during install: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("List blocked behind an install's copy")
	}

	// Let the copy finish: one writer, then the read side proceeds.
	writer, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-installed; err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "slow", "plugin.json")); err != nil {
		t.Fatalf("plugin not installed: %v", err)
	}
	if left := stageDirs(t, root); len(left) != 0 {
		t.Fatalf("staging directories left behind: %v", left)
	}
}

// TestConcurrentInstallsOfOneIDLeaveOnePlugin: both installs stage their
// own copy, and the destination check under the lock is what makes the
// second one a refusal instead of an overwrite.
func TestConcurrentInstallsOfOneIDLeaveOnePlugin(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	src := t.TempDir()
	writePlugin(t, src, ".", map[string]any{
		"id": "race", "name": "Race", "version": "1.0.0",
		"entry": "dist/index.js", "permissions": []string{},
	}, "console.log('race')")

	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := store.Install(src)
			errs <- err
		}()
	}
	var refused int
	for range 2 {
		if err := <-errs; err != nil {
			if !strings.Contains(err.Error(), "already installed") {
				t.Fatalf("unexpected install error: %v", err)
			}
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("refused installs = %d, want exactly one", refused)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "race" {
		t.Fatalf("registry = %+v, want exactly one race plugin", list)
	}
	if left := stageDirs(t, root); len(left) != 0 {
		t.Fatalf("staging directories left behind: %v", left)
	}
}

// TestInstallFailureRemovesItsStaging pins the other end of the staging
// step: a plugin that fails preparation leaves the registry as it was,
// with no half-copied directory and no staging leftover.
func TestInstallFailureRemovesItsStaging(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	src := t.TempDir()
	// The manifest declares a kraft binary that the source does not
	// ship, so preparation fails after the copy already ran.
	writePlugin(t, src, ".", map[string]any{
		"id": "broken", "name": "Broken", "version": "1.0.0",
		"entry": "dist/index.js", "permissions": []string{},
		"kraft": map[string]any{"binary": "bin/missing", "protocol": 1},
	}, "console.log('broken')")

	if _, err := store.Install(src); err == nil {
		t.Fatal("install of a plugin with no kraft binary succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "broken")); !os.IsNotExist(err) {
		t.Fatalf("a failed install left a plugin directory: %v", err)
	}
	if left := stageDirs(t, root); len(left) != 0 {
		t.Fatalf("staging directories left behind: %v", left)
	}
}
