package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

func TestInlineLocalMedia(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(png, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	var localSource media.ImageSource
	if err := json.Unmarshal(
		[]byte(`{"kind":"url","url":`+strconv.Quote(png)+`,"media_type":"image/png"}`),
		&localSource,
	); err != nil {
		t.Fatalf("build local image source: %v", err)
	}
	remoteSource, err := media.NewImageURL("https://example.com/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parts, changed, err := inlineLocalMedia([]message.Part{
		message.TextPart{Text: "look"},
		message.ImagePart{Source: localSource},
		message.ImagePart{Source: remoteSource},
		message.FilePart{URI: png, Name: "photo.png", MediaType: "image/png"},
	})
	if err != nil {
		t.Fatalf("inlineLocalMedia: %v", err)
	}
	if !changed {
		t.Fatal("inlineLocalMedia reported no change")
	}
	local := parts[1].(message.ImagePart)
	if local.Source.Kind() != media.SourceInline {
		t.Fatalf("local image source = %s, want inline", local.Source.Kind())
	}
	if string(local.Source.Bytes()) != "png-bytes" {
		t.Errorf("inline bytes = %q, want source bytes", local.Source.Bytes())
	}
	remote := parts[2].(message.ImagePart)
	if remote.Source.Kind() != media.SourceURL {
		t.Errorf("remote image was inlined: %s", remote.Source.Kind())
	}
	if _, ok := parts[3].(message.FilePart); !ok {
		t.Errorf("file part changed type: %T", parts[3])
	}
}

func TestInlineLocalMediaNoChange(t *testing.T) {
	remote, err := media.NewImageURL("https://example.com/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parts, changed, err := inlineLocalMedia([]message.Part{
		message.TextPart{Text: "hi"},
		message.ImagePart{Source: remote},
	})
	if err != nil {
		t.Fatalf("inlineLocalMedia: %v", err)
	}
	if changed || len(parts) != 2 {
		t.Errorf("changed=%v parts=%d, want no change", changed, len(parts))
	}
}

func TestFlattenNonImageMedia(t *testing.T) {
	const workDir = "/Users/test/Workspace/proj"
	const storedAudio = workDir + "/.opencraft/sessions/s-abc/media/1-a.mp3"
	const storedVideo = workDir + "/.opencraft/sessions/s-abc/media/2-b.mp4"
	const storedFile = workDir + "/.opencraft/sessions/s-abc/files/3-notes.txt"
	var audioSource media.AudioSource
	if err := json.Unmarshal(
		[]byte(`{"kind":"url","url":`+strconv.Quote(storedAudio)+`,"media_type":"audio/mpeg"}`),
		&audioSource,
	); err != nil {
		t.Fatal(err)
	}
	var videoSource media.VideoSource
	if err := json.Unmarshal(
		[]byte(`{"kind":"url","url":`+strconv.Quote(storedVideo)+`,"media_type":"video/mp4"}`),
		&videoSource,
	); err != nil {
		t.Fatal(err)
	}
	parts, changed := flattenNonImageMedia([]message.Part{
		message.TextPart{Text: "look"},
		message.FilePart{URI: storedFile, Name: "notes.txt"},
		message.AudioPart{Source: audioSource},
		message.VideoPart{Source: videoSource},
	}, workDir)
	if !changed || len(parts) != 4 {
		t.Fatalf("flatten = %v parts, changed=%v", len(parts), changed)
	}
	if got := parts[1].(message.TextPart).Text; got != "[一般文件] .opencraft/sessions/s-abc/files/3-notes.txt" {
		t.Errorf("file line = %q", got)
	}
	if got := parts[2].(message.TextPart).Text; got != "[音频文件] .opencraft/sessions/s-abc/media/1-a.mp3" {
		t.Errorf("audio line = %q", got)
	}
	if got := parts[3].(message.TextPart).Text; got != "[视频文件] .opencraft/sessions/s-abc/media/2-b.mp4" {
		t.Errorf("video line = %q", got)
	}

	// Paths outside the workspace stay absolute.
	var outside media.AudioSource
	if err := json.Unmarshal(
		[]byte(`{"kind":"url","url":"/tmp/out.mp3","media_type":"audio/mpeg"}`),
		&outside,
	); err != nil {
		t.Fatal(err)
	}
	parts, changed = flattenNonImageMedia([]message.Part{
		message.AudioPart{Source: outside},
	}, workDir)
	if !changed || len(parts) != 1 {
		t.Fatalf("outside flatten = %v parts, changed=%v", len(parts), changed)
	}
	if got := parts[0].(message.TextPart).Text; got != "[音频文件] /tmp/out.mp3" {
		t.Errorf("outside line = %q", got)
	}

	// Images survive stripping.
	remote, err := media.NewImageURL("https://example.com/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parts, changed = flattenNonImageMedia([]message.Part{
		message.TextPart{Text: "hi"},
		message.ImagePart{Source: remote},
	}, workDir)
	if changed || len(parts) != 2 {
		t.Errorf("flatten = %v parts, changed=%v, want unchanged", len(parts), changed)
	}
}
