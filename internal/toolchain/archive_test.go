package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExtractsBundledArchiveOnDemand(t *testing.T) {
	root, cache := filepath.Join(t.TempDir(), "runtime"), filepath.Join(t.TempDir(), "cache")
	platform := platformKey()
	archiveData := testTar(t, "python-3.13.15/bin/python3", "python-3.13.15/lib/site.py")
	archivePath := filepath.Join(root, "archives", "python", platform)
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archiveData, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archiveData)
	manifest := map[string]any{
		"schema_version": 1,
		"python": map[string]any{
			"version": "3.13.15",
			"urls":    map[string]string{platform: "https://example.invalid/python.tar.gz"},
			"sha256":  map[string]string{platform: hex.EncodeToString(sum[:])},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	setExternalPath(t, filepath.Join(t.TempDir(), "empty"))

	m, err := New(Options{
		Preference:   PreferenceExternalFirst,
		Root:         root,
		ManifestPath: filepath.Join(root, "manifest.json"),
		CacheDir:     cache,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := m.Resolve(context.Background(), "python3")
	if err != nil {
		t.Fatalf("Resolve(python3): %v", err)
	}
	if got.Source != SourceBundled || got.Family != "python" {
		t.Fatalf("python3 = %+v, want bundled python", got)
	}
	wantPath := filepath.Join(
		cache, "python", "3.13.15", platform, "bin", "python3")
	if got.Path != wantPath {
		t.Fatalf("resolved path = %q, want %q", got.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(
		cache, "python", "3.13.15", platform, "lib", "site.py",
	)); err != nil {
		t.Fatalf("extracted archive missing lib/site.py: %v", err)
	}
}

func TestResolveRejectsChecksumMismatch(t *testing.T) {
	root, cache := filepath.Join(t.TempDir(), "runtime"), filepath.Join(t.TempDir(), "cache")
	platform := platformKey()
	archivePath := filepath.Join(root, "archives", "node", platform)
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": 1,
		"node": map[string]any{
			"version": "24.20.0",
			"urls":    map[string]string{platform: "https://example.invalid/node.tar.gz"},
			"sha256": map[string]string{
				platform: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	setExternalPath(t, filepath.Join(t.TempDir(), "empty"))
	m, err := New(Options{
		Preference:   PreferenceExternalFirst,
		Root:         root,
		ManifestPath: filepath.Join(root, "manifest.json"),
		CacheDir:     cache,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Resolve(context.Background(), "node"); err == nil {
		t.Fatal("Resolve(node) with bad checksum should fail")
	}
}

func testTar(t *testing.T, files ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range files {
		body := []byte("#!/bin/sh\nexit 0\n")
		if filepath.Ext(name) == ".py" {
			body = []byte("# site\n")
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
