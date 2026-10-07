package bindings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAppStorageIsItsOwn pins ctx.storage's scope. An application's
// key/value space is a namespace inside its own state root, so the same
// key written by two installations holds two values, a missing key reads
// as empty rather than as an error, and the file is where an uninstall
// with purge takes it from.
func TestAppStorageIsItsOwn(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install hello: %v", err)
	}
	// The same package under a second id: two installations, two state
	// roots, one key.
	if _, err := f.binding.Install(
		writeAppBundle(t), AppInstallOptions{ID: "other"},
	); err != nil {
		t.Fatalf("install other: %v", err)
	}

	if err := f.binding.KVSet("hello", "theme", "dark"); err != nil {
		t.Fatalf("set hello: %v", err)
	}
	if err := f.binding.KVSet("other", "theme", "light"); err != nil {
		t.Fatalf("set other: %v", err)
	}
	if err := f.binding.KVSet("hello", "count", "2"); err != nil {
		t.Fatalf("set count: %v", err)
	}

	got, err := f.binding.KVGet("hello", "theme")
	if err != nil {
		t.Fatalf("get hello: %v", err)
	}
	if got.Key != "theme" || got.Value != "dark" {
		t.Fatalf("hello.theme = %+v, want dark", got)
	}
	other, err := f.binding.KVGet("other", "theme")
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if other.Value != "light" {
		t.Fatalf("other.theme = %q, want light", other.Value)
	}

	entries, err := f.binding.KVList("hello")
	if err != nil {
		t.Fatalf("list hello: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("hello storage = %+v, want two entries", entries)
	}

	// A key nothing wrote reads as an empty value: a bundle asking
	// whether it has stored something gets an answer, not a failure.
	missing, err := f.binding.KVGet("hello", "never-written")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing.Value != "" {
		t.Fatalf("missing key = %q, want empty", missing.Value)
	}

	// The file is one per application, under that application's state
	// root — which is what makes the isolation structural rather than a
	// convention the store layer enforces.
	for _, id := range []string{"hello", "other"} {
		status, err := f.binding.Status(id)
		if err != nil {
			t.Fatalf("status %s: %v", id, err)
		}
		// Spelled out rather than read from the constant: the layout is
		// the plugin store's, and a test that builds the path from the
		// code under test would agree with any namespace at all.
		path := filepath.Join(status.StateRoot, ".data", "kv", "kv.json")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}

	if err := f.binding.KVDelete("hello", "theme"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := f.binding.KVGet("hello", "theme")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone.Value != "" {
		t.Fatalf("deleted key = %q, want empty", gone.Value)
	}
	if kept, err := f.binding.KVGet("other", "theme"); err != nil || kept.Value != "light" {
		t.Fatalf("other.theme after hello's delete = %+v (%v)", kept, err)
	}

	// Disabling keeps the storage: an application that stops serving
	// starts again with what it remembered.
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if count, err := f.binding.KVGet("hello", "count"); err != nil || count.Value != "2" {
		t.Fatalf("count across a disable/enable = %+v (%v)", count, err)
	}

	// Uninstalling without purge keeps it too — the user asked for the
	// application to go, not for their data to.
	if err := f.binding.Uninstall("hello", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "apps", "hello", ".data")); err != nil {
		t.Fatalf("state after a plain uninstall: %v", err)
	}
}

// TestAppStorageStoreIsSharedPerApplication pins the one thing the cache
// in App.kv is for. A KV store carries its own mutex, so a fresh one per
// call would hand two concurrent writers two locks and let their
// read-modify-write cycles interleave — one of the writes would be lost
// without either call failing. One store per application is what makes
// ctx.storage serialize.
func TestAppStorageStoreIsSharedPerApplication(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := f.binding.Install(
		writeAppBundle(t), AppInstallOptions{ID: "other"},
	); err != nil {
		t.Fatalf("install other: %v", err)
	}

	first, err := f.binding.kv("hello")
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	second, err := f.binding.kv("hello")
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	if first != second {
		t.Fatal("two calls handed out two stores for one application")
	}
	other, err := f.binding.kv("other")
	if err != nil {
		t.Fatalf("kv other: %v", err)
	}
	if other == first {
		t.Fatal("two applications share one store")
	}
}
