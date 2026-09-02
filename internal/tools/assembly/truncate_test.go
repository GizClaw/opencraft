package assembly

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestTruncateMiddlewarePersistsFullOutputAndTruncates(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, ".opencraft", "cache", "tools")
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 200,
		Dir:      dir,
		WorkDir:  work,
	})
	if mw == nil {
		t.Fatal("middleware must not be nil when enabled")
	}

	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{CallID: "call-1", Content: strings.Repeat("x", 1000)}
	}
	res := mw(next)(context.Background(), message.ToolCall{})

	if len([]rune(res.Content)) > 200 {
		t.Fatalf("truncated content = %d runes, want <= 200", len([]rune(res.Content)))
	}
	if !strings.Contains(res.Content, "truncated; full output:") {
		t.Fatalf("marker missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, filepath.Join(".opencraft", "cache", "tools", "call-1.output")) {
		t.Fatalf("relative pointer missing: %q", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "call-1.output"))
	if err != nil {
		t.Fatalf("full output not persisted: %v", err)
	}
	if string(raw) != strings.Repeat("x", 1000) {
		t.Fatalf("persisted output = %d bytes, want 1000", len(raw))
	}
}

func TestTruncateMiddlewarePassesSmallResultsThrough(t *testing.T) {
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 100,
		Dir:      t.TempDir(),
	})
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{CallID: "call-1", Content: "short"}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	if res.Content != "short" {
		t.Fatalf("content = %q, want untouched", res.Content)
	}
}

func TestTruncateMiddlewareDisabled(t *testing.T) {
	if mw := truncateMiddleware(TruncateSettings{Enabled: false, MaxChars: 10, Dir: t.TempDir()}); mw != nil {
		t.Fatal("disabled middleware must be nil")
	}
	if mw := truncateMiddleware(TruncateSettings{Enabled: true, MaxChars: 0, Dir: t.TempDir()}); mw != nil {
		t.Fatal("misconfigured middleware must be nil")
	}
}

func TestTruncateMiddlewareSkipsErrors(t *testing.T) {
	mw := truncateMiddleware(TruncateSettings{Enabled: true, MaxChars: 5, Dir: t.TempDir()})
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{
			CallID:  "call-1",
			Content: strings.Repeat("e", 100),
			IsError: true,
		}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	if len([]rune(res.Content)) != 100 {
		t.Fatalf("error result must pass through untouched, got %d runes", len([]rune(res.Content)))
	}
}

func TestTruncateCacheOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".opencraft", "cache", "tools")
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 10,
		Dir:      dir,
	})
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{CallID: "call-1", Content: strings.Repeat("x", 100)}
	}
	mw(next)(context.Background(), message.ToolCall{})

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("cache dir mode = %o, want 700", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "call-1.output"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("cache file mode = %o, want 600", perm)
	}
}
