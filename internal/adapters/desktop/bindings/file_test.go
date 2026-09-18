package bindings

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/utils/filetype"
)

func TestFileListAndSearch(t *testing.T) {
	root := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	b := NewFileBinding(c)

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes, err := b.List(".", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "main.go" {
		t.Fatalf("nodes = %+v", nodes)
	}
	hits, err := b.Search("main", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "main.go" {
		t.Fatalf("hits = %+v", hits)
	}
}

// Hidden entries stay out of the tree and the quick-open search until
// ui.showHiddenFiles asks for them; once it does, both surfaces list
// and match them so the tree and the search box agree.
func TestFileListAndSearchFollowShowHidden(t *testing.T) {
	root := t.TempDir()
	c := core.NewCore(t.TempDir(), t.TempDir(), root)
	b := NewFileBinding(c)

	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, ".github", "workflows", "ci.yml"),
		[]byte("x"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	nodes, err := b.List(".", false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	if len(names) != 0 {
		t.Fatalf("hidden entries leaked into the default listing: %v", names)
	}
	if hits, err := b.Search("ci.yml", 10, false); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Fatalf("hidden entries leaked into the default search: %+v", hits)
	}

	nodes, err = b.List(".", true)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	// Dirs first, then files; both hidden entries are part of the listing.
	if len(names) != 2 || names[0] != ".github" || names[1] != ".env" {
		t.Fatalf("names = %v", names)
	}

	nested, err := b.List(".github", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 1 || nested[0].Path != ".github/workflows" {
		t.Fatalf("nested = %+v", nested)
	}

	hits, err := b.Search("workflows/ci", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != ".github/workflows/ci.yml" {
		t.Fatalf("hits = %+v", hits)
	}
	envHits, err := b.Search(".env", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(envHits) != 1 || envHits[0].Path != ".env" {
		t.Fatalf("env hits = %+v", envHits)
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
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, pngBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)
	att, err := b.ReadAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if att.Path != src || att.MediaType != "image/png" || att.DataURL == "" {
		t.Fatalf("attachment = %+v", att)
	}
}

// TestReadAttachmentClassifiesByContent pins what an attachment reports
// for the composer: the frontend builds image/audio/video parts from the
// media type, so the type has to follow the bytes. Source code under a
// .ts or .mp4 name must not stage as a video, and a picture under a text
// name must still stage as an image.
func TestReadAttachmentClassifiesByContent(t *testing.T) {
	workDir := t.TempDir()
	sources := map[string]string{
		"app.ts":   "export const answer = 42;\n",
		"clip.mp4": "not a video at all\n",
		"logo.svg": "<svg viewBox=\"0 0 1 1\"></svg>\n",
	}
	for name, content := range sources {
		if err := os.WriteFile(
			filepath.Join(workDir, name), []byte(content), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "screenshot.txt"), pngBuf.Bytes(), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	for name := range sources {
		att, err := b.ReadAttachment(filepath.Join(workDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if family := filetype.Family(att.MediaType); family != "" ||
			att.DataURL != "" {
			t.Errorf("%s attachment = %+v, want no media family", name, att)
		}
	}

	att, err := b.ReadAttachment(filepath.Join(workDir, "screenshot.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if att.MediaType != "image/png" || att.DataURL == "" {
		t.Fatalf("attachment = %+v, want the PNG its bytes are", att)
	}
}

func TestReadAttachmentAllowsLargeNonImage(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "report.pdf")
	size := (10 << 20) + 1
	if err := os.WriteFile(src, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	att, err := b.ReadAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if att.Size != int64(size) {
		t.Fatalf("size = %d, want %d", att.Size, size)
	}
	if att.DataURL != "" {
		t.Fatalf("non-image attachment must not carry a data URL")
	}
}

func TestReadAttachmentRejectsOversizedImage(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "photo.png")
	size := (10 << 20) + 1
	if err := os.WriteFile(src, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	if _, err := b.ReadAttachment(src); err == nil {
		t.Fatal("oversized image unexpectedly accepted for preview")
	}
}

func TestReadAttachmentServesSmallImageByteForByte(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "photo.png")
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	att, err := b.ReadAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if att.MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png", att.MediaType)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(
		encoded.Bytes())
	if att.DataURL != want {
		t.Fatal("small image preview must embed the original bytes")
	}
}

func TestReadAttachmentNormalizesOversizedStillImageToJPEG(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "photo.png")
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	// Pad past the preview cap. PNG decoders stop at IEND, so the
	// trailing bytes keep the file decodable while exercising the
	// normalize path that lifts the original-size gate.
	padded := append(encoded.Bytes(), make([]byte, (10<<20)+1-len(encoded.Bytes()))...)
	if err := os.WriteFile(src, padded, 0o600); err != nil {
		t.Fatal(err)
	}
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	b := NewFileBinding(c)

	att, err := b.ReadAttachment(src)
	if err != nil {
		t.Fatalf("oversized still image preview failed: %v", err)
	}
	if !strings.HasPrefix(att.DataURL, "data:image/jpeg;base64,") {
		t.Fatalf("preview data URL is not a normalized jpeg: %.40s", att.DataURL)
	}
}

func TestImportPastedImagePNG(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	b := NewFileBinding(c)
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
	dataURL := "data:image/png;base64," +
		base64.StdEncoding.EncodeToString(encoded.Bytes())

	att, err := b.ImportPastedImage("Pasted image.png", dataURL)
	if err != nil {
		t.Fatalf("ImportPastedImage: %v", err)
	}
	defer os.Remove(att.Path)
	if att.Name != "Pasted image.png" {
		t.Fatalf("name = %q", att.Name)
	}
	if att.MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png", att.MediaType)
	}
	data, err := os.ReadFile(att.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, encoded.Bytes()) {
		t.Fatal("staged paste bytes differ from the clipboard payload")
	}
	if !strings.HasPrefix(att.DataURL, "data:image/png;base64,") {
		t.Fatalf("preview missing: %.40s", att.DataURL)
	}
}

func TestImportPastedImageRejectsInvalidPayloads(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	b := NewFileBinding(c)
	for _, tc := range []struct {
		name, dataURL string
	}{
		{name: "not a data URL", dataURL: "/tmp/paste.png"},
		{name: "not an image", dataURL: "data:text/plain;base64,AA=="},
		{name: "invalid base64", dataURL: "data:image/png;base64,!!!"},
		{name: "unsupported image type",
			dataURL: "data:image/x-icon;base64,AA=="},
	} {
		if _, err := b.ImportPastedImage("p.png", tc.dataURL); err == nil {
			t.Fatalf("%s unexpectedly accepted", tc.name)
		}
	}
}

func TestImportPastedImageEnforcesSizeCap(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), t.TempDir())
	b := NewFileBinding(c)
	payload := make([]byte, maxPastedImageBytes+1)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)
	if _, err := b.ImportPastedImage("big.png", dataURL); err == nil {
		t.Fatal("oversized pasted image unexpectedly accepted")
	}
}
