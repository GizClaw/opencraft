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
		return toolmiddleware.ResultLimiter(s.MaxChars), nil
	}
	return toolmiddleware.ResultLimiter(
		s.MaxChars, toolmiddleware.WithResultMarker(s.Marker)), nil
}
