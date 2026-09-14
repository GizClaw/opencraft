package assembly

import (
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
)

// ResultLimitSettings caps every tool result at MaxChars Unicode code
// points before it reaches the truncate/audit stages. Zero MaxChars
// disables the middleware; a negative value is a configuration error.
type ResultLimitSettings struct {
	MaxChars int    `json:"max_chars,omitempty"`
	Marker   string `json:"marker,omitempty"`
	// PartBudgetBytes caps the encoded size of the result's non-text
	// parts (images, audio, structured data). Absent keeps flowcraft's
	// default (1 MiB); 0 lifts the cap. Keep a view_image deployment's
	// downscale target at or below this, or the image is dropped before
	// the model sees it.
	PartBudgetBytes *int `json:"part_budget_bytes,omitempty"`
}

func resultLimitMiddleware(s *ResultLimitSettings) (tool.Middleware, error) {
	if s == nil || s.MaxChars == 0 {
		return nil, nil
	}
	if s.MaxChars < 0 {
		return nil, errdefs.Validationf(
			"tool middleware: result_limit.max_chars must be positive, got %d",
			s.MaxChars)
	}
	if s.Marker == "" {
		opts := make([]toolmiddleware.ResultLimitOption, 0, 1)
		if s.PartBudgetBytes != nil {
			opts = append(opts,
				toolmiddleware.WithResultPartBudget(*s.PartBudgetBytes))
		}
		return toolmiddleware.ResultLimiter(s.MaxChars, opts...), nil
	}
	opts := []toolmiddleware.ResultLimitOption{
		toolmiddleware.WithResultMarker(s.Marker),
	}
	if s.PartBudgetBytes != nil {
		opts = append(opts,
			toolmiddleware.WithResultPartBudget(*s.PartBudgetBytes))
	}
	return toolmiddleware.ResultLimiter(s.MaxChars, opts...), nil
}
