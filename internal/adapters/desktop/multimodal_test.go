package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestDecodeMessage(t *testing.T) {
	remote, err := media.NewImageURL("https://example.com/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	msg := message.Message{
		Role: message.RoleUser,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "what is this?"},
			message.ImagePart{Source: remote},
		}},
	}
	err = validateUserMessage(msg)
	if err != nil {
		t.Fatalf("validateUserMessage: %v", err)
	}
	if len(msg.Content.Parts) != 2 {
		t.Fatalf("decoded message = %+v", msg)
	}
	if _, ok := msg.Content.Parts[0].(message.TextPart); !ok {
		t.Errorf("part 0 is %T, want TextPart", msg.Content.Parts[0])
	}
	img, ok := msg.Content.Parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("part 1 is %T, want ImagePart", msg.Content.Parts[1])
	}
	if img.Source.Kind() != media.SourceURL {
		t.Errorf("image source kind = %s, want url", img.Source.Kind())
	}
}

func TestDecodeMessageRejectsNonUser(t *testing.T) {
	msg := message.Message{
		Role:    message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{message.TextPart{Text: "hi"}}},
	}
	if err := validateUserMessage(msg); err == nil {
		t.Error("validateUserMessage accepted assistant role")
	}
}

func TestPersistAttachments(t *testing.T) {
	store, err := ocsessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srcDir := t.TempDir()
	imgSrc := filepath.Join(srcDir, "photo.png")
	if err := os.WriteFile(imgSrc, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	txtSrc := filepath.Join(srcDir, "notes.txt")
	if err := os.WriteFile(txtSrc, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var imgSource media.ImageSource
	if err := newURLSource(imgSrc, "image/png", &imgSource); err != nil {
		t.Fatalf("build local image source: %v", err)
	}
	parts := []message.Part{
		message.TextPart{Text: "look"},
		message.ImagePart{Source: imgSource},
		message.FilePart{URI: txtSrc, MediaType: "text/plain", Name: "notes.txt"},
	}
	got, err := persistAttachments(store, id, parts)
	if err != nil {
		t.Fatalf("persistAttachments: %v", err)
	}
	img := got[1].(message.ImagePart)
	if img.Source.Kind() != media.SourceURL {
		t.Fatalf("image source = %s, want url", img.Source.Kind())
	}
	if !strings.Contains(img.Source.URL(), string(filepath.Separator)+"media"+string(filepath.Separator)) {
		t.Errorf("image stored outside media dir: %q", img.Source.URL())
	}
	file := got[2].(message.FilePart)
	if file.URI != txtSrc {
		t.Errorf("file URI = %q, want original path %q", file.URI, txtSrc)
	}
	if _, err := os.Stat(img.Source.URL()); err != nil {
		t.Errorf("stored image missing: %v", err)
	}
	// Non-image files are not copied into the session.
	sessionDir, err := store.RolloutPath(id)
	if err != nil {
		t.Fatalf("session dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(sessionDir), "files")); !os.IsNotExist(err) {
		t.Errorf("files dir exists after persisting a file part (err=%v)", err)
	}
}

func TestReadAttachment(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := (&App{}).ReadAttachment(png)
	if err != nil {
		t.Fatalf("ReadAttachment(image): %v", err)
	}
	if img.MediaType != "image/png" || !strings.HasPrefix(img.DataURL, "data:image/png;base64,") {
		t.Errorf("image dto = %+v", img)
	}
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := (&App{}).ReadAttachment(txt)
	if err != nil {
		t.Fatalf("ReadAttachment(file): %v", err)
	}
	if file.DataURL != "" || file.MediaType != "text/plain" || file.Size != 5 {
		t.Errorf("file dto = %+v", file)
	}
}
