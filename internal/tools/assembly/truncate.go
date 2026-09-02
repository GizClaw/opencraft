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
			runes := []rune(res.Content)
			if len(runes) <= cfg.MaxChars {
				return res
			}
			path := filepath.Join(cfg.Dir, res.CallID+".output")
			if err := os.MkdirAll(cfg.Dir, 0o700); err == nil {
				_ = os.Chmod(cfg.Dir, 0o700)
				tmp := path + ".tmp"
				if err := os.WriteFile(tmp, []byte(res.Content), 0o600); err == nil {
					_ = os.Rename(tmp, path)
				}
			}
			ref := path
			if cfg.WorkDir != "" {
				if rel, err := filepath.Rel(cfg.WorkDir, path); err == nil {
					ref = rel
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
			res.Content = string(out)
			return res
		}
	}
}
