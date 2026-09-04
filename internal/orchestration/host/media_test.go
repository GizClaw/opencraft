package host

import (
	"os"
	"path/filepath"
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
	defer func() { _ = store.Close() }()

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
