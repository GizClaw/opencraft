package bindings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

func TestFileListAndSearch(t *testing.T) {
	root := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	b := NewFileBinding(c)

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes, err := b.List(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "main.go" {
		t.Fatalf("nodes = %+v", nodes)
	}
	hits, err := b.Search("main", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "main.go" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestRenderPatchNeverReturnsNilLines(t *testing.T) {
	root := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	b := NewFileBinding(c)
	files, err := b.RenderPatch(`*** Begin Patch
*** Delete File: missing.txt
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Lines == nil {
		t.Fatal("RenderPatch must return an empty lines slice, not null")
	}
}

func TestReadAttachmentOutsideWorkspace(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "photo.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)
	att, err := b.ReadAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if att.Path != src || att.DataURL == "" {
		t.Fatalf("attachment = %+v", att)
	}
}
