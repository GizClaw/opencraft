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
	"github.com/GizClaw/opencraft/internal/foundation/utils/imageutil"
)

// persistUserAttachments makes URL-sourced image attachments durable
// for the session archive. Supported still images are normalized to
// JPEG q90 (EXIF applied, transparency flattened) before the bytes
// land in the session's media/ directory and the part URL is
// rewritten to the stored path; audio/video/file parts keep their
// original absolute path (the model reads the live file, so the
// session stays light). Remote URLs and inline bytes pass through
// untouched.
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
					dst, resolvedType, err := saveUserImageAttachment(
						store, id, src, p.Source.MediaType())
					if err != nil {
						return nil, err
					}
					source, err := newLocalURLSource(
						dst, resolvedType,
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

// maxInlineImageBytes mirrors the media prepare hook's inline limit.
// Normalized JPEGs below this are persisted; anything else (an
// undecodable format, an animated GIF, or a compression result that
// still does not fit the prompt budget) falls back to the original
// copy path with its own 10 MiB cap.
const maxInlineImageBytes = 10 << 20

// saveUserImageAttachment persists one local image as session media.
// Supported still images are re-encoded as JPEG q90 (EXIF orientation
// applied, transparency flattened) so large PNG/TIFF/BMP attachments
// no longer hit the original file-size gate. GIF/WebP/AVIF and other
// formats the decoder cannot normalize keep the original bytes.
func saveUserImageAttachment(
	store *ocsessions.Store,
	id, src, mediaType string,
) (string, string, error) {
	if mediaType != "image/gif" {
		data, err := imageutil.NormalizeFileToJPEG(src)
		if err == nil && len(data) <= maxInlineImageBytes {
			if dst, saveErr := store.SaveAttachmentBytes(
				id, "media", "image.jpg", data,
			); saveErr == nil {
				return dst, "image/jpeg", nil
			}
		}
	}
	dst, err := store.SaveAttachment(id, "media", src)
	if err != nil {
		return "", "", err
	}
	return dst, mediaTypeOr(src, mediaType), nil
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
