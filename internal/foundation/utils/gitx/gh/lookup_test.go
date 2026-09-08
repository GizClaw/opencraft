package gh

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeGHBinary(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func lookupOK(bin string) func(string) (string, error) {
	return func(string) (string, error) { return bin, nil }
}

func lookupMiss(string) (string, error) {
	return "", errors.New("not on PATH")
}

// TestGHEnvironmentResolution pins the lookup precedence: an explicit
// GH_PATH override wins, a broken override falls through to PATH, PATH
// misses fall back to well-known install locations, and nothing usable
// yields ErrNoProvider.
func TestGHEnvironmentResolution(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "pinned-gh")
	onPath := filepath.Join(dir, "on-path-gh")
	candidate := filepath.Join(dir, "candidate-gh")
	writeGHBinary(t, pinned, 0o755)
	writeGHBinary(t, onPath, 0o755)
	writeGHBinary(t, candidate, 0o755)

	got, err := ghExecutableFrom(
		lookupOK(onPath), pinned, []string{candidate})
	if err != nil {
		t.Fatalf("resolve with pin: %v", err)
	}
	if got != pinned {
		t.Fatalf("binary = %q, want pinned %q", got, pinned)
	}

	brokenPin := filepath.Join(dir, "missing-gh")
	got, err = ghExecutableFrom(
		lookupOK(onPath), brokenPin, []string{candidate})
	if err != nil {
		t.Fatalf("resolve with broken pin: %v", err)
	}
	if got != onPath {
		t.Fatalf("binary = %q, want PATH %q", got, onPath)
	}

	got, err = ghExecutableFrom(
		lookupMiss, "", []string{candidate})
	if err != nil {
		t.Fatalf("resolve with candidate fallback: %v", err)
	}
	if got != candidate {
		t.Fatalf("binary = %q, want candidate %q", got, candidate)
	}

	if _, err := ghExecutableFrom(lookupMiss, "", nil); !errors.Is(
		err, ErrNoProvider) {
		t.Fatalf("resolve with no candidates err = %v, want ErrNoProvider", err)
	}

	if runtime.GOOS == "windows" {
		return // no executable permission bit to test
	}
	notExecutable := filepath.Join(dir, "plain-gh")
	writeGHBinary(t, notExecutable, 0o644)
	if _, err := ghExecutableFrom(
		lookupMiss, "", []string{notExecutable}); !errors.Is(
		err, ErrNoProvider) {
		t.Fatalf("resolve with non-executable err = %v, want ErrNoProvider", err)
	}
}
