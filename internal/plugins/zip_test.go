package plugins

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallZip(t *testing.T) {
	manifest := `{
		"id": "zip-plugin",
		"name": "Zip Plugin",
		"version": "1.0.0",
		"entry": "dist/index.js",
		"capability": {"binary": "bin/auth", "protocol": 1}
	}`
	zipPath := writeTestZip(t, map[string]string{
		"zip-plugin-1.0.0/plugin.json":   manifest,
		"zip-plugin-1.0.0/dist/index.js": "export function apply() {}",
		"zip-plugin-1.0.0/bin/auth":      "#!/bin/sh\necho hi\n",
		"zip-plugin-1.0.0/README.md":     "test",
	})
	store := NewStore(t.TempDir())
	sum, err := store.InstallZip(zipPath)
	if err != nil {
		t.Fatalf("InstallZip: %v", err)
	}
	if sum.ID != "zip-plugin" || !sum.Enabled {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := store.Bundle("zip-plugin"); err != nil {
		t.Fatalf("bundle after zip install: %v", err)
	}
}

func TestInstallZipRejectsTraversal(t *testing.T) {
	zipPath := writeTestZip(t, map[string]string{
		"plugin.json":    `{"id":"z","name":"Z","version":"1","entry":"dist/index.js"}`,
		"../outside.txt": "evil",
	})
	store := NewStore(t.TempDir())
	if _, err := store.InstallZip(zipPath); err == nil {
		t.Fatal("zip traversal must fail")
	}
}
