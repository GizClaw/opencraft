package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "opencraft.yaml")
	if err := writeFileAtomic(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a: 1\n" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// No temp litter remains in the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "opencraft.yaml" {
			t.Fatalf("unexpected leftover %s", e.Name())
		}
	}
}

func TestNewStableID(t *testing.T) {
	a, b := NewStableID(), NewStableID()
	if a == "" || b == "" {
		t.Fatal("stable ids must not be empty")
	}
	if a == b {
		t.Fatalf("stable ids must differ: %q", a)
	}
	if !strings.HasPrefix(a, "inst-") {
		t.Fatalf("stable id %q has no inst- prefix", a)
	}
}
