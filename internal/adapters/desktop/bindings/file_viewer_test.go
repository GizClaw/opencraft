package bindings

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
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

	// A large text file reports TooLarge instead of crossing IPC. The
	// bytes decide here too: padding the file with NULs would make it
	// binary, and binary files fall back to the metadata pane.
	big := filepath.Join(workDir, "big.txt")
	line := []byte("a line of text\n")
	lines := make([]byte, 0, previewTextLimit+len(line))
	for len(lines) <= previewTextLimit {
		lines = append(lines, line...)
	}
	if err := os.WriteFile(big, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	pb, err := b.ReadPreview("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !pb.TooLarge || pb.Text != "" {
		t.Fatalf("oversize preview = %+v", pb)
	}

	zeros := filepath.Join(workDir, "big.bin")
	if err := os.WriteFile(zeros, make([]byte, previewTextLimit+1), 0o644); err != nil {
		t.Fatal(err)
	}
	pz, err := b.ReadPreview("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if pz.TooLarge || pz.Kind != "meta" {
		t.Fatalf("oversize binary preview = %+v", pz)
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

// TestReadPreviewRendersTextWhateverTheName pins the text side of the
// classification: the payload decides, so every one of these opens in
// the code viewer even though its name means something else to the
// platform table. macOS and Linux map .ts and .mts to video/mp2t — the
// MPEG transport stream — .rs and .sh to application/* types the text
// renderer refuses, .svg to an image and .mp4 to a video; .json and .md
// are text formats the table may call anything at all.
func TestReadPreviewRendersTextWhateverTheName(t *testing.T) {
	workDir := t.TempDir()
	sources := map[string]string{
		"app.ts":      "export const answer = 42;\n",
		"module.mts":  "export const answer = 42;\n",
		"main.rs":     "fn main() {}\n",
		"deploy.sh":   "#!/bin/sh\necho hi\n",
		"query.sql":   "select 1;\n",
		"diagram.svg": "<svg viewBox=\"0 0 1 1\"></svg>\n",
		"config.json": "{\"answer\": 42}\n",
		"notes.md":    "# Notes\n",
		// A text name under a media name: the bytes win, so neither the
		// video path nor the image path claims it.
		"clip.mp4": "not a video at all\n",
		"shot.png": "not a picture at all\n",
	}
	for name, content := range sources {
		path := filepath.Join(workDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)
	b.SetMediaURL(func(rel string) (string, error) {
		t.Errorf("%s was offered as a video stream", rel)
		return "http://127.0.0.1:1/media/" + rel, nil
	})

	for name, content := range sources {
		p, err := b.ReadPreview(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind != "text" || p.Text != content || p.StreamURL != "" ||
			p.DataURL != "" || p.TooLarge {
			t.Errorf("%s preview = %+v", name, p)
		}
	}
}

// TestReadPreviewDecidesMediaByContentNotExtension pins the other
// direction: a name cannot hide what the bytes are. Every case pairs a
// name whose platform mapping says something else with payloads that
// carry a signature the classifier can read.
func TestReadPreviewDecidesMediaByContentNotExtension(t *testing.T) {
	workDir := t.TempDir()
	var pngBuf, jpegBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBuf, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	// An mp4 with a real ftyp box: the sniffer names video/mp4 on every
	// platform, whatever the file is called.
	clip := append([]byte{0x00, 0x00, 0x00, 0x20}, []byte("ftypisom")...)
	clip = append(clip, 0x00, 0x00, 0x00, 0x00)
	clip = append(clip, []byte("mp41")...)
	clip = append(clip, bytes.Repeat([]byte{0x42}, 32)...)
	// One MPEG transport stream packet, the content .ts really means.
	packet := append([]byte{0x47, 0x40, 0x00, 0x10, 0x00}, make([]byte, 183)...)

	files := map[string][]byte{
		"screenshot.txt": pngBuf.Bytes(),
		"photo.png":      jpegBuf.Bytes(),
		"clip.txt":       clip,
		"recording.ts":   packet,
	}
	for name, data := range files {
		if err := os.WriteFile(
			filepath.Join(workDir, name), data, 0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)
	b.SetMediaURL(func(rel string) (string, error) {
		return "http://127.0.0.1:1/media/" + rel, nil
	})

	// A picture named .txt is still a picture, and it is embedded as the
	// type its bytes carry.
	ps, err := b.ReadPreview("screenshot.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Kind != "image" || ps.MediaType != "image/png" ||
		!strings.HasPrefix(ps.DataURL, "data:image/png;base64,") {
		t.Errorf("PNG named .txt preview = %+v", ps)
	}

	// A JPEG named .png previews as the JPEG its bytes are.
	pj, err := b.ReadPreview("photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if pj.Kind != "image" || pj.MediaType != "image/jpeg" ||
		!strings.HasPrefix(pj.DataURL, "data:image/jpeg;base64,") {
		t.Errorf("JPEG named .png preview = %+v", pj)
	}

	// A video named .txt streams from the loopback URL: the media stack
	// is handed video/mp4, not the text type the name suggests.
	pv, err := b.ReadPreview("clip.txt")
	if err != nil {
		t.Fatal(err)
	}
	if pv.Kind != "video" || pv.MediaType != "video/mp4" ||
		pv.StreamURL != "http://127.0.0.1:1/media/clip.txt" {
		t.Errorf("mp4 named .txt preview = %+v", pv)
	}

	// A .ts recording is not source code: it never reaches the text
	// renderer. Its exact type follows the platform table (video/mp2t
	// where .ts is known, the sniffed fallback where it is not), so only
	// the text verdict is pinned.
	pr, err := b.ReadPreview("recording.ts")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Kind == "text" || pr.Text != "" {
		t.Errorf("transport stream preview = %+v", pr)
	}
}

// TestReadPreviewStreamsWorkspaceVideo pins the video path: a workspace
// video becomes a streamed preview (kind video + loopback URL) instead
// of an inline data URL, and stays metadata-only when no stream builder
// is wired.
func TestReadPreviewStreamsWorkspaceVideo(t *testing.T) {
	workDir := t.TempDir()
	clip := filepath.Join(workDir, "generated", "clip.mp4")
	if err := os.MkdirAll(filepath.Dir(clip), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clip, []byte("\x00\x00\x00\x18ftypisom"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	plain, err := b.ReadPreview("generated/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Kind != "meta" || plain.StreamURL != "" {
		t.Fatalf("preview without a stream builder = %+v, want meta", plain)
	}

	var gotRel string
	b.SetMediaURL(func(rel string) (string, error) {
		gotRel = rel
		return "http://127.0.0.1:1/media/token/" + rel, nil
	})
	streamed, err := b.ReadPreview("generated/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if gotRel != "generated/clip.mp4" {
		t.Errorf("stream builder rel = %q", gotRel)
	}
	if streamed.Kind != "video" ||
		streamed.StreamURL != "http://127.0.0.1:1/media/token/generated/clip.mp4" {
		t.Fatalf("video preview = %+v", streamed)
	}
	if streamed.DataURL != "" || streamed.TooLarge {
		t.Fatalf("video preview must not embed bytes: %+v", streamed)
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

	nodes, err := b.List(".", false)
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

	docs, err := b.List("docs", false)
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

	if _, err := b.List("..", false); err == nil {
		t.Fatal("listing the parent of the workspace unexpectedly allowed")
	}
	if _, err := b.List(dataDir, false); err == nil {
		t.Fatal("listing a readable root outside the workspace unexpectedly allowed")
	}
}
