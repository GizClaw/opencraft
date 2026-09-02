package assembly

import (
	"context"
	"regexp"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
)

func FuzzRedactContent(f *testing.F) {
	for _, seed := range []string{
		"sk-1234567890abcdef",
		"sk-proj-1234567890abcdefghijklmnop",
		"AKIA1234567890ABCDEF",
		"plain text",
		"",
	} {
		f.Add(seed)
	}
	rules := []toolmiddleware.RedactRule{
		{Pattern: regexp.MustCompile("sk-(?:proj-)?[A-Za-z0-9_-]{16,}")},
		{Pattern: regexp.MustCompile("AKIA[0-9A-Z]{16}")},
	}
	mw := toolmiddleware.Redact(rules...)
	f.Fuzz(func(t *testing.T, input string) {
		res := mw(func(context.Context, message.ToolCall) message.ToolResult {
			return message.ToolResult{Content: input}
		})(context.Background(), message.ToolCall{})
		if res.Content == "" && input != "" {
			t.Fatalf("redaction dropped a non-empty result")
		}
	})
}
