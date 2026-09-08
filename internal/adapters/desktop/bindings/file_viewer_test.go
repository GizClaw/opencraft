package bindings

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
)

func TestResolveTargetFileAndDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	f, err := b.ResolveTarget("main.go", "")
	if err != nil {
		t.Fatalf("resolve file: %v", err)
	}
	if f.IsDir || f.Rel != "main.go" || f.Root != "workspace" || f.Name != "main.go" {
		t.Fatalf("file target = %+v", f)
	}

	d, err := b.ResolveTarget(".", "")
	if err != nil {
		t.Fatalf("resolve dir: %v", err)
	}
	if !d.IsDir || d.Rel != "." || d.Name != filepath.Base(workDir) {
		t.Fatalf("dir target = %+v", d)
	}

	if _, err := b.ResolveTarget("missing.go", ""); err == nil {
		t.Fatal("missing target unexpectedly resolved")
	}
}

func TestResolveTargetUsesDocumentBase(t *testing.T) {
	workDir := t.TempDir()
	dir := filepath.Join(workDir, "docs")
	if err := os.MkdirAll(filepath.Join(dir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "refs", "guide.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	f, err := b.ResolveTarget("refs/guide.md", "docs")
	if err != nil {
		t.Fatalf("resolve under base: %v", err)
	}
	if f.Rel != "docs/refs/guide.md" {
		t.Fatalf("rel = %q", f.Rel)
	}
}

func TestResolveTargetRejectsTraversal(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	if _, err := b.ResolveTarget("../secret.txt", ""); err == nil {
		t.Fatal("traversal outside workspace unexpectedly allowed")
	}
	if _, err := b.ResolveTarget("../secret.txt", "docs/.."); err == nil {
		t.Fatal("traversal through base unexpectedly allowed")
	}
}

func TestResolveTargetRejectsSymlinkEscape(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(workDir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	if _, err := b.ResolveTarget("link.txt", ""); err == nil {
		t.Fatal("symlink escape unexpectedly allowed")
	}
}

func TestResolveTargetAllowsDataRootAbsolute(t *testing.T) {
	dataDir := t.TempDir()
	workDir := t.TempDir()
	media := filepath.Join(dataDir, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(media, "photo.png")
	if err := os.WriteFile(src, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), dataDir, workDir)
	b := NewFileBinding(c)

	f, err := b.ResolveTarget(src, "")
	if err != nil {
		t.Fatalf("resolve data-root file: %v", err)
	}
	if f.Root != "data" || f.Rel != "" {
		t.Fatalf("target = %+v", f)
	}

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ResolveTarget(secret, ""); err == nil {
		t.Fatal("absolute path outside every root unexpectedly allowed")
	}
}

func TestReadPreviewTextAndOversize(t *testing.T) {
	workDir := t.TempDir()
	src := filepath.Join(workDir, "note.md")
	content := "# Hello\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	p, err := b.ReadPreview("note.md")
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != content || p.TooLarge || p.Rel != "note.md" {
		t.Fatalf("preview = %+v", p)
	}

	big := filepath.Join(workDir, "big.txt")
	if err := os.WriteFile(big, make([]byte, previewTextLimit+1), 0o644); err != nil {
		t.Fatal(err)
	}
	pb, err := b.ReadPreview("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !pb.TooLarge || pb.Text != "" {
		t.Fatalf("oversize preview = %+v", pb)
	}
}

func TestReadPreviewBinaryImageAndMetadata(t *testing.T) {
	workDir := t.TempDir()

	bin := filepath.Join(workDir, "blob.bin")
	if err := os.WriteFile(bin, []byte{0, 1, 2, 0}, 0o644); err != nil {
		t.Fatal(err)
	}

	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(workDir, "photo.png")
	if err := os.WriteFile(photo, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := filepath.Join(workDir, "report.pdf")
	if err := os.WriteFile(doc, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	pb, err := b.ReadPreview("blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if pb.Text != "" || pb.TooLarge {
		t.Fatalf("binary preview = %+v", pb)
	}

	pi, err := b.ReadPreview("photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pi.DataURL, "data:image/png;base64,") {
		t.Fatalf("image preview missing: %.40s", pi.DataURL)
	}

	pd, err := b.ReadPreview("report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if pd.MediaType != "application/pdf" || pd.Text != "" ||
		!strings.HasPrefix(pd.DataURL, "data:application/pdf;base64,") ||
		pd.TooLarge {
		t.Fatalf("pdf preview = %+v", pd)
	}
}

func TestListReturnsRelativePaths(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "docs", "guide.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	nodes, err := b.List(".")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes {
		got = append(got, n.Path)
	}
	if len(got) != 2 || got[0] != "docs" || got[1] != "main.go" {
		t.Fatalf("paths = %v", got)
	}

	docs, err := b.List("docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Path != "docs/guide.md" {
		t.Fatalf("docs paths = %+v", docs)
	}
}

func TestListRejectsTraversal(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	c := core.NewCore(t.TempDir(), dataDir, workDir)
	b := NewFileBinding(c)

	if _, err := b.List(".."); err == nil {
		t.Fatal("listing the parent of the workspace unexpectedly allowed")
	}
	if _, err := b.List(dataDir); err == nil {
		t.Fatal("listing a readable root outside the workspace unexpectedly allowed")
	}
}
