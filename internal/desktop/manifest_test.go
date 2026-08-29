package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestManifestSnapshotSkipsHeavyDirs verifies the walk records regular
// files while excluding the same directories as the file panel.
func TestManifestSnapshotSkipsHeavyDirs(t *testing.T) {
	wd := t.TempDir()
	writeTestFile(t, filepath.Join(wd, "docs", "report.md"), "report")
	writeTestFile(t, filepath.Join(wd, "main.go"), "package main")
	writeTestFile(t, filepath.Join(wd, "node_modules", "pkg", "index.js"), "x")
	writeTestFile(t, filepath.Join(wd, ".git", "HEAD"), "ref")
	writeTestFile(t, filepath.Join(wd, ".opencraft", "sessions", "s-1", "meta.json"), "{}")
	writeTestFile(t, filepath.Join(wd, "dist", "out.js"), "y")

	snap, err := manifestSnapshot(context.Background(), wd)
	if err != nil {
		t.Fatalf("manifestSnapshot: %v", err)
	}
	for _, want := range []string{"docs/report.md", "main.go"} {
		if _, ok := snap[want]; !ok {
			t.Errorf("snapshot missing %q", want)
		}
	}
	for _, absent := range []string{
		"node_modules/pkg/index.js",
		".git/HEAD",
		".opencraft/sessions/s-1/meta.json",
		"dist/out.js",
	} {
		if _, ok := snap[absent]; ok {
			t.Errorf("snapshot must exclude %q", absent)
		}
	}
}

// TestDiffDocumentArtifacts verifies new/changed document files are
// reported (sorted), unchanged files and code files are not.
func TestDiffDocumentArtifacts(t *testing.T) {
	before := map[string]fileStat{
		"docs/a.md":   {Size: 10, ModNs: 1},
		"main.go":     {Size: 20, ModNs: 1},
		"docs/b.docx": {Size: 100, ModNs: 1},
	}
	after := map[string]fileStat{
		"docs/a.md":   {Size: 12, ModNs: 2},
		"main.go":     {Size: 20, ModNs: 1},
		"docs/b.docx": {Size: 100, ModNs: 1},
		"docs/c.pdf":  {Size: 50, ModNs: 1},
		"src/new.go":  {Size: 5, ModNs: 1},
	}
	got := diffDocumentArtifacts(before, after)
	want := []ocsessions.Artifact{
		{Path: "docs/a.md", Bytes: 12},
		{Path: "docs/c.pdf", Bytes: 50},
	}
	if len(got) != len(want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("diff[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
