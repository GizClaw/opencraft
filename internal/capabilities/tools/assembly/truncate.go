// TruncateSettings caps oversized tool results: the full output is
// persisted under <workdir>/.opencraft/cache/tools/<call_id>.output and
// the in-context content is replaced with a head+tail excerpt plus a
// pointer to the file, so the model can read the rest on demand.
package assembly

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
)

// TruncateSettings configures the truncation middleware. Zero values
// disable it.
type TruncateSettings struct {
	// Enabled turns truncation on.
	Enabled bool `json:"enabled,omitempty"`
	// MaxChars is the in-context cap measured in Unicode code points.
	MaxChars int `json:"max_chars,omitempty"`
	// Dir is the directory that receives full outputs
	// (<dir>/<call_id>.output).
	Dir string `json:"dir,omitempty"`
	// WorkDir anchors the relative pointer written into the result.
	WorkDir string `json:"work_dir,omitempty"`
}

// truncateMiddleware persists full outputs and truncates oversized
// results. A nil middleware is returned when the feature is disabled
// or misconfigured, so callers can skip it.
func truncateMiddleware(cfg TruncateSettings) tool.Middleware {
	if !cfg.Enabled || cfg.MaxChars <= 0 || cfg.Dir == "" {
		return nil
	}
	return func(next tool.Dispatch) tool.Dispatch {
		return func(ctx context.Context, call message.ToolCall) message.ToolResult {
			res := next(ctx, call)
			if res.IsError {
				return res
			}
			// The cap counts the text projection: non-text parts
			// (images, audio, structured data) are carried through
			// untouched instead of being flattened away.
			full := res.Content.Text()
			runes := []rune(full)
			if len(runes) <= cfg.MaxChars {
				return res
			}
			path := filepath.Join(cfg.Dir, res.CallID+".output")
			if err := os.MkdirAll(cfg.Dir, 0o700); err == nil {
				telemetry.WarnErr(ctx,
					"tool assembly: secure truncation directory failed",
					os.Chmod(cfg.Dir, 0o700))
				tmp := path + ".tmp"
				if err := os.WriteFile(tmp, []byte(full), 0o600); err == nil {
					telemetry.WarnErr(ctx,
						"tool assembly: persist truncated output failed",
						os.Rename(tmp, path))
				} else {
					telemetry.WarnErr(ctx,
						"tool assembly: write truncated output failed", err)
				}
			} else {
				telemetry.WarnErr(ctx,
					"tool assembly: create truncation directory failed", err)
			}
			ref := path
			if cfg.WorkDir != "" {
				if rel, err := filepath.Rel(cfg.WorkDir, path); err == nil {
					ref = rel
				} else {
					telemetry.WarnErr(ctx,
						"tool assembly: resolve relative truncation path failed",
						err)
				}
			}
			markerRunes := []rune(fmt.Sprintf("\n…[truncated; full output: %s]", ref))
			if len(markerRunes) > cfg.MaxChars {
				markerRunes = markerRunes[:cfg.MaxChars]
			}
			keep := cfg.MaxChars - len(markerRunes)
			head := keep * 7 / 10
			tail := keep - head
			out := make([]rune, 0, cfg.MaxChars)
			out = append(out, runes[:head]...)
			out = append(out, markerRunes...)
			out = append(out, runes[len(runes)-tail:]...)
			res.Content = replaceTextParts(res.Content, string(out))
			return res
		}
	}
}

// replaceTextParts swaps every text part for one truncated text part
// and keeps the remaining parts in order, so a multimodal result loses
// prose and keeps its media.
func replaceTextParts(content message.Content, text string) message.Content {
	out := message.Content{
		Parts: make([]message.Part, 0, len(content.Parts)+1),
	}
	out.Parts = append(out.Parts, message.TextPart{Text: text})
	for _, part := range content.Parts {
		normalized, err := message.NormalizePart(part)
		if err != nil {
			continue
		}
		if _, isText := normalized.(message.TextPart); isText {
			continue
		}
		out.Parts = append(out.Parts, part)
	}
	return out
}
