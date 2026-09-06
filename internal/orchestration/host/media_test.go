package host

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestPersistUserAttachmentsCopiesLocalImages(t *testing.T) {
	src := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()

	source, err := newLocalURLSource(src, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := ocsessions.NewID()
	parts, err := persistUserAttachments(
		store, id, []message.Part{message.ImagePart{Source: source}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
	img, ok := parts[0].(message.ImagePart)
	if !ok {
		t.Fatalf("part = %T, want ImagePart", parts[0])
	}
	if img.Source.URL() == src {
		t.Fatal("image URL was not rewritten to the session media dir")
	}
	if _, err := os.Stat(img.Source.URL()); err != nil {
		t.Fatalf("stored image missing: %v", err)
	}
}

func TestPersistUserAttachmentsNormalizesSupportedImageToJPEG(t *testing.T) {
	src := filepath.Join(t.TempDir(), "photo.png")
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
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()

	source, err := newLocalURLSource(src, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := ocsessions.NewID()
	parts, err := persistUserAttachments(
		store, id, []message.Part{message.ImagePart{Source: source}},
	)
	if err != nil {
		t.Fatal(err)
	}
	imgPart := parts[0].(message.ImagePart)
	stored := imgPart.Source.URL()
	if !strings.HasSuffix(stored, ".jpg") {
		t.Fatalf("stored path = %q, want .jpg", stored)
	}
	if imgPart.Source.MediaType() != "image/jpeg" {
		t.Fatalf("media type = %q, want image/jpeg", imgPart.Source.MediaType())
	}
	data, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		t.Fatalf("stored bytes are not a JPEG: %x", data[:min(len(data), 4)])
	}
}
