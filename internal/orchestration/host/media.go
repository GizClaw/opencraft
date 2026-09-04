package host

import (
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// persistUserAttachments makes URL-sourced image attachments durable
// for the session archive. Image bytes are copied into the session's
// media/ directory and the part URL is rewritten to the stored path;
// audio/video/file parts keep their original absolute path (the model
// reads the live file, so the session stays light). Remote URLs and
// inline bytes pass through untouched.
func persistUserAttachments(
	store *ocsessions.Store,
	id string,
	parts []message.Part,
) ([]message.Part, error) {
	if store == nil {
		return parts, nil
	}
	out := make([]message.Part, 0, len(parts))
	for _, part := range parts {
		normalized, err := message.NormalizePart(part)
		if err != nil {
			return nil, err
		}
		switch p := normalized.(type) {
		case message.ImagePart:
			if p.Source.Kind() == media.SourceURL {
				if src, ok := localFilePath(p.Source.URL()); ok {
					dst, err := store.SaveAttachment(id, "media", src)
					if err != nil {
						return nil, err
					}
					source, err := newLocalURLSource(
						dst, mediaTypeOr(src, p.Source.MediaType()),
					)
					if err != nil {
						return nil, err
					}
					out = append(out, message.ImagePart{Source: source})
					continue
				}
			}
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return out, nil
}

// newLocalURLSource builds a URL-kind media source whose URL is a
// local filesystem path. The typed constructors reject non-URL paths,
// so the source is assembled through the same wire form the archive
// and the frontend use.
func newLocalURLSource(path, mediaType string) (media.ImageSource, error) {
	raw, err := json.Marshal(struct {
		Kind      string `json:"kind"`
		URL       string `json:"url"`
		MediaType string `json:"media_type,omitempty"`
	}{Kind: "url", URL: path, MediaType: mediaType})
	if err != nil {
		return media.ImageSource{}, err
	}
	var source media.ImageSource
	if err := json.Unmarshal(raw, &source); err != nil {
		return media.ImageSource{}, err
	}
	return source, nil
}

// localFilePath resolves a part URL to a local file path when it
// points at an existing regular file. http(s)://, data: URIs, and
// missing paths return ok=false so remote sources pass through.
func localFilePath(raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", false
	}
	path = strings.TrimPrefix(path, "file://")
	if strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") ||
		strings.HasPrefix(path, "data:") {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// mediaTypeOr returns mediaType when non-empty, otherwise the type
// derived from path's extension.
func mediaTypeOr(path, mediaType string) string {
	if mediaType != "" {
		return mediaType
	}
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		return strings.TrimSpace(t)
	}
	return ""
}
