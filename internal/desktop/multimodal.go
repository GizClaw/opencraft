package desktop

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// maxAttachmentBytes caps one attachment the desktop reads for preview
// (mirrors sessions.maxAttachmentBytes).
const maxAttachmentBytes = 10 << 20

// validateUserMessage validates one StartTurn message: it must be a
// user message with at least one content part. The binding decodes the
// wire form (message.Content.UnmarshalJSON) before this runs.
func validateUserMessage(msg message.Message) error {
	if strings.TrimSpace(string(msg.Role)) == "" {
		return errdefs.Validationf("message role is required")
	}
	if msg.Role != message.RoleUser {
		return errdefs.Validationf(
			"StartTurn accepts user messages only, got %q", msg.Role)
	}
	if len(msg.Content.Parts) == 0 {
		return errdefs.Validationf("message content is required")
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	return nil
}

// hasMediaParts reports whether parts carry anything besides plain
// text (image/audio/video/file/data).
func hasMediaParts(parts []message.Part) bool {
	for _, part := range parts {
		switch part.(type) {
		case message.TextPart, message.ReasoningPart:
			continue
		default:
			return true
		}
	}
	return false
}

// persistAttachments makes the URL sources durable for the session
// archive. Only images are copied into the session's media/ directory
// (their bytes must survive for resume rendering); the part URL is
// rewritten to the stored path. Audio/video/file parts keep their
// original absolute path — the model sees the live file and the
// session stays light. The model-facing inline conversion happens
// later in the opencraft.media prepare hook. Remote URLs and inline
// bytes pass through untouched.
func persistAttachments(
	store *ocsessions.Store, id string, parts []message.Part,
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
					var source media.ImageSource
					if err := newURLSource(dst, mediaTypeOr(src, p.Source.MediaType()), &source); err != nil {
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

// newURLSource fills dst with a URL-kind media source whose URL is a
// local path. media.NewImageURL / NewAudioURL / NewVideoURL reject
// non-URL paths (they require a scheme and host), so the source is
// assembled through the same wire form the archive and the frontend
// use.
func newURLSource(path, mediaType string, dst any) error {
	raw, err := json.Marshal(struct {
		Kind      string `json:"kind"`
		URL       string `json:"url"`
		MediaType string `json:"media_type,omitempty"`
	}{Kind: "url", URL: path, MediaType: mediaType})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// localFilePath resolves a part URL to a local file path when it
// points at an existing regular file. http(s)://, data: URIs, and
// missing paths return ok=false so remote sources pass through.
func localFilePath(raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "file://") {
		path = strings.TrimPrefix(path, "file://")
	}
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

// ReadAttachment returns preview metadata for one local attachment.
// Image files also carry a base64 data: URI so the WKWebView frontend
// can render thumbnails without file:// access; other files return
// name/size/media type only (the spec: images preview, everything else
// renders as a file chip).
func (a *App) ReadAttachment(path string) (AttachmentDTO, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return AttachmentDTO{}, errdefs.Validationf("attachment path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return AttachmentDTO{}, err
	}
	if !info.Mode().IsRegular() {
		return AttachmentDTO{}, errdefs.Validationf("%s is not a regular file", path)
	}
	if info.Size() > maxAttachmentBytes {
		return AttachmentDTO{}, errdefs.Validationf(
			"attachment too large to preview (%d bytes)", info.Size())
	}
	dto := AttachmentDTO{
		Name:      filepath.Base(path),
		Path:      path,
		Size:      info.Size(),
		MediaType: mediaTypeOr(path, ""),
	}
	if strings.HasPrefix(dto.MediaType, "image/") {
		data, err := os.ReadFile(path)
		if err != nil {
			return AttachmentDTO{}, err
		}
		dto.DataURL = "data:" + dto.MediaType + ";base64," +
			base64.StdEncoding.EncodeToString(data)
	}
	return dto, nil
}
