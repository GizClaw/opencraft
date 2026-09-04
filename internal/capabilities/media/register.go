// Package media implements the opencraft.media prepare hook: it
// flattens non-image parts (audio/video/file) into text lines like
// "[音频文件] …" / "[视频文件] …" / "[一般文件] …" and converts
// URL-sourced image parts into inline base64 before the engine reads
// the board. Image is the only true multimodal input today; the other
// attachments stay visible to the model as paths (which the file tools
// can read) without relying on driver support for audio / video / file
// parts. The desktop copies only images into the session's media/
// directory (resume rendering needs the bytes); audio/video/file parts
// keep their original absolute path. The archived request carries the
// same URL/file form, so resume re-renders every attachment with its
// real kind.
package media

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/resource"
)

// maxInlineBytes caps one local media file the prepare hook inlines.
// The desktop caps persisted attachments at the same size, so this
// only trips for custom graphs that inject their own URL sources.
const maxInlineBytes = 10 << 20

// Register adds the opencraft.media prepare hook factory to r.
func Register(r *resource.Registry) error {
	return r.Register(prepareFactory{})
}

// prepareFactory builds the opencraft.media prepare hook.
type prepareFactory struct{}

var _ resource.Factory = prepareFactory{}

type prepareSettings struct {
	// WorkDir is the workspace root; attachment paths under it are
	// rendered relative to it in the flattened text.
	WorkDir string `json:"work_dir"`
}

func (prepareFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.prepare",
		Impl: "opencraft.media",
	}
}

func (prepareFactory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[prepareSettings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	return agent.PreparerFunc(func(
		ctx context.Context, id agent.Identity, req *agent.Request, prev *agent.Board,
	) (*agent.Board, error) {
		if req == nil || prev == nil {
			return nil, errdefs.Validationf(
				"media: request and previous board are required")
		}
		channel := prev.Channel(agent.MainChannel)
		// seedBoard appends the user's request as channel[0] before
		// the preparer chain runs, so the first message is the turn
		// input. req.Message itself is left in URL form: the commit
		// hook archives it, keeping the session archive compact.
		if len(channel) == 0 || channel[0].Role != message.RoleUser {
			return prev, nil
		}
		parts, flattened := flattenNonImageMedia(channel[0].Content.Parts, settings.WorkDir)
		parts, inlined, err := inlineLocalMedia(parts)
		if err != nil {
			return prev, err
		}
		if !flattened && !inlined {
			return prev, nil
		}
		first := channel[0]
		first.Content.Parts = parts
		channel[0] = first
		prev.SetChannel(agent.MainChannel, channel)
		return prev, nil
	}), nil
}

// inlineLocalMedia rewrites URL-sourced image/audio/video parts whose
// URL points at a local file into inline base64 parts. Remote URLs
// (http/https/data:) and non-media parts pass through unchanged.
func inlineLocalMedia(parts []message.Part) ([]message.Part, bool, error) {
	out := make([]message.Part, 0, len(parts))
	changed := false
	for _, part := range parts {
		normalized, err := message.NormalizePart(part)
		if err != nil {
			return nil, false, err
		}
		switch p := normalized.(type) {
		case message.ImagePart:
			if p.Source.Kind() == media.SourceURL {
				if path, ok := localPath(p.Source.URL()); ok {
					if err := checkSize(path); err != nil {
						return nil, false, err
					}
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, false, fmt.Errorf(
							"media: read image %s: %w", path, err)
					}
					source, err := media.NewImageBytes(data, mediaTypeOr(path, p.Source.MediaType()))
					if err != nil {
						return nil, false, err
					}
					out = append(out, message.ImagePart{Source: source})
					changed = true
					continue
				}
			}
			out = append(out, p)
		case message.AudioPart:
			if p.Source.Kind() == media.SourceURL {
				if path, ok := localPath(p.Source.URL()); ok {
					if err := checkSize(path); err != nil {
						return nil, false, err
					}
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, false, fmt.Errorf(
							"media: read audio %s: %w", path, err)
					}
					source, err := media.NewAudioBytes(data, mediaTypeOr(path, p.Source.MediaType()))
					if err != nil {
						return nil, false, err
					}
					out = append(out, message.AudioPart{
						Source: source, Format: p.Format, DurationMillis: p.DurationMillis,
					})
					changed = true
					continue
				}
			}
			out = append(out, p)
		case message.VideoPart:
			if p.Source.Kind() == media.SourceURL {
				if path, ok := localPath(p.Source.URL()); ok {
					if err := checkSize(path); err != nil {
						return nil, false, err
					}
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, false, fmt.Errorf(
							"media: read video %s: %w", path, err)
					}
					source, err := media.NewVideoBytes(data, mediaTypeOr(path, p.Source.MediaType()))
					if err != nil {
						return nil, false, err
					}
					out = append(out, message.VideoPart{Source: source})
					changed = true
					continue
				}
			}
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return out, changed, nil
}

// flattenNonImageMedia rewrites audio/video/file parts in the
// model-facing message into text lines carrying the stored workspace
// path, so any driver can accept the turn. The archive keeps the
// original typed parts (the commit hook restores req.Message), so
// resume re-renders attachments with their real kind. Image and text
// parts pass through untouched.
func flattenNonImageMedia(parts []message.Part, workDir string) ([]message.Part, bool) {
	out := make([]message.Part, 0, len(parts)+1)
	changed := false
	for _, part := range parts {
		normalized, err := message.NormalizePart(part)
		if err != nil {
			out = append(out, part)
			continue
		}
		switch p := normalized.(type) {
		case message.AudioPart:
			changed = true
			out = append(out, message.TextPart{
				Text: "[音频文件] " + workspacePath(workDir, p.Source.URL()),
			})
		case message.VideoPart:
			changed = true
			out = append(out, message.TextPart{
				Text: "[视频文件] " + workspacePath(workDir, p.Source.URL()),
			})
		case message.FilePart:
			changed = true
			out = append(out, message.TextPart{
				Text: "[一般文件] " + workspacePath(workDir, p.URI),
			})
		default:
			out = append(out, p)
		}
	}
	if !changed {
		return parts, false
	}
	return out, true
}

// workspacePath renders an attachment path relative to the workspace
// when it lives under it (the model sees ".opencraft/sessions/..."),
// and falls back to the absolute path otherwise.
func workspacePath(workDir, path string) string {
	if workDir != "" {
		if rel, err := filepath.Rel(workDir, path); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return path
}

func localPath(raw string) (string, bool) {
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

func checkSize(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > maxInlineBytes {
		return fmt.Errorf(
			"media: %s exceeds the %d-byte inline limit", path, maxInlineBytes)
	}
	return nil
}
