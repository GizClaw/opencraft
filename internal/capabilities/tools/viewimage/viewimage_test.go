package viewimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/disintegration/imaging"
)

func newWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return ws
}

// writeImage stores one generated image and returns its pixel size.
func writeImage(
	t *testing.T, ws workspace.Workspace, path string, w, h int,
) {
	t.Helper()
	img := imaging.New(w, h, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	img = imaging.Overlay(
		img,
		imaging.New(w/2, h/2, color.NRGBA{R: 20, G: 200, B: 120, A: 255}),
		image.Pt(w/4, h/4),
		1,
	)
	var buf bytes.Buffer
	if err := imaging.Encode(&buf, img, imaging.PNG); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := ws.Write(context.Background(), path, buf.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestViewImageReturnsImagePart(t *testing.T) {
	ws := newWorkspace(t)
	writeImage(t, ws, "small.png", 320, 200)
	tool, err := New(ws, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := tool.Execute(context.Background(), `{"path":"small.png"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(content.Parts) != 2 {
		t.Fatalf("parts = %d, want caption + image", len(content.Parts))
	}
	if _, ok := content.Parts[0].(message.TextPart); !ok {
		t.Fatalf("first part = %T, want text caption", content.Parts[0])
	}
	part, ok := content.Parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("second part = %T, want image", content.Parts[1])
	}
	if part.Source.Kind() != media.SourceInline {
		t.Fatalf("source kind = %v, want inline", part.Source.Kind())
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(part.Source.Bytes()))
	if err != nil {
		t.Fatalf("decode returned image: %v", err)
	}
	if cfg.Width != 320 || cfg.Height != 200 {
		t.Fatalf("dimensions = %dx%d, want the source size", cfg.Width, cfg.Height)
	}
}

// TestViewImageDownscalesOversizedImages pins the auto-resize contract:
// an image beyond the edge target still comes back, smaller, instead of
// being rejected or dropped by the part budget.
func TestViewImageDownscalesOversizedImages(t *testing.T) {
	ws := newWorkspace(t)
	writeImage(t, ws, "big.png", 3000, 2000)
	tool, err := New(ws, Settings{MaxEdge: 800, MaxBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	content, err := tool.Execute(context.Background(), `{"path":"big.png"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	part, ok := content.Parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("part = %T, want image", content.Parts[1])
	}
	data := part.Source.Bytes()
	if len(data) > 64<<10 {
		t.Fatalf("encoded size = %d, over the budget", len(data))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 800 || cfg.Height > 800 {
		t.Fatalf("dimensions = %dx%d, want longest edge <= 800", cfg.Width, cfg.Height)
	}
}

func TestViewImageRejectsNonImagesAndMissingFiles(t *testing.T) {
	ws := newWorkspace(t)
	if err := ws.Write(context.Background(), "notes.txt", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	tool, err := New(ws, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{
		`{"path":"notes.txt"}`,
		`{"path":"missing.png"}`,
		`{"path":""}`,
		`{}`,
	} {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Fatalf("args %s: expected an error", args)
		}
	}
}

// limitedWorkspace records the bound the tool asks for, so a test can
// pin that an oversized file is never materialized.
type limitedWorkspace struct {
	workspace.Workspace
	reader workspace.LimitedReader
	limit  int64
}

func (w *limitedWorkspace) ReadLimited(
	ctx context.Context, path string, maxBytes int64,
) ([]byte, error) {
	w.limit = maxBytes
	return w.reader.ReadLimited(ctx, path, maxBytes)
}

// TestViewImageReadsOversizedFilesBounded pins the read contract: a
// workspace with bounded reads is asked for the source cap itself, so a
// huge file is rejected without being read into memory.
func TestViewImageReadsOversizedFilesBounded(t *testing.T) {
	inner := newWorkspace(t)
	reader, ok := inner.(workspace.LimitedReader)
	if !ok {
		t.Fatalf("test workspace %T does not support bounded reads", inner)
	}
	ws := &limitedWorkspace{Workspace: inner, reader: reader}
	huge := make([]byte, maxSourceBytes+1024)
	if err := inner.Write(context.Background(), "huge.png", huge); err != nil {
		t.Fatal(err)
	}
	tool, err := New(ws, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(
		context.Background(), `{"path":"huge.png"}`,
	); err == nil {
		t.Fatal("oversized image must be rejected")
	}
	if ws.limit != int64(maxSourceBytes) {
		t.Fatalf(
			"bounded read limit = %d, want %d",
			ws.limit, int64(maxSourceBytes),
		)
	}
}
