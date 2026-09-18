package assembly

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
		return message.ToolResult{CallID: "call-1", Content: message.NewTextContent(strings.Repeat("x", 1000))}
	}
	res := mw(next)(context.Background(), message.ToolCall{})

	if len([]rune(res.Content.Text())) > 200 {
		t.Fatalf("truncated content = %d runes, want <= 200", len([]rune(res.Content.Text())))
	}
	if !strings.Contains(res.Content.Text(), "truncated; full output:") {
		t.Fatalf("marker missing: %q", res.Content.Text())
	}
	if !strings.Contains(res.Content.Text(), filepath.Join(".opencraft", "cache", "tools", "call-1.output")) {
		t.Fatalf("relative pointer missing: %q", res.Content.Text())
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
		return message.ToolResult{CallID: "call-1", Content: message.NewTextContent("short")}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	if res.Content.Text() != "short" {
		t.Fatalf("content = %q, want untouched", res.Content.Text())
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
			Content: message.NewTextContent(strings.Repeat("e", 100)),
			IsError: true,
		}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	if len([]rune(res.Content.Text())) != 100 {
		t.Fatalf("error result must pass through untouched, got %d runes", len([]rune(res.Content.Text())))
	}
}

// read_file and friends answer with a JSON envelope. Truncation must
// keep that envelope parseable: the UI (and the model) previously got
// an escaped JSON blob because the marker's raw newline was spliced
// into the middle of a JSON string.
func TestTruncateMiddlewareKeepsJSONEnvelopeValid(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, ".opencraft", "cache", "tools")
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 400,
		Dir:      dir,
		WorkDir:  work,
	})
	content := "HEAD-MARKER\n" +
		strings.Repeat("line with \"quotes\", <tags> and \\slashes\n", 60) +
		"TAIL-MARKER\n"
	full, err := json.Marshal(map[string]any{
		"file_path":    "internal/capabilities/execd/server.go",
		"content":      content,
		"offset":       1,
		"limit":        420,
		"total_lines":  637,
		"is_truncated": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{
			CallID:  "call-1",
			Content: message.NewTextContent(string(full)),
		}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	out := res.Content.Text()
	if got := len([]rune(out)); got > 400 {
		t.Fatalf("truncated result = %d runes, want <= 400", got)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("truncated result is not valid JSON: %v\n%s", err, out)
	}
	if got := envelope["file_path"]; got != "internal/capabilities/execd/server.go" {
		t.Fatalf("file_path = %v, want the original path", got)
	}
	if got := envelope["total_lines"]; got != float64(637) {
		t.Fatalf("total_lines = %v, want 637", got)
	}
	if got := envelope["is_truncated"]; got != true {
		t.Fatalf("is_truncated = %v, want true after middleware truncation", got)
	}
	excerpt, _ := envelope["content"].(string)
	if !strings.Contains(excerpt, "truncated; full output:") {
		t.Fatalf("marker missing from content: %q", excerpt)
	}
	if !strings.HasPrefix(excerpt, "HEAD-MARKER") {
		t.Fatalf("head of the content missing: %q", excerpt)
	}
	if !strings.HasSuffix(excerpt, "TAIL-MARKER\n") {
		t.Fatalf("tail of the content missing: %q", excerpt)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "call-1.output"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(full) {
		t.Fatal("persisted output must stay byte-identical to the full result")
	}
}

func TestTruncateMiddlewareExcerptsEachOversizedJSONString(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, ".opencraft", "cache", "tools")
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 400,
		Dir:      dir,
		WorkDir:  work,
	})
	full, err := json.Marshal(map[string]any{
		"exit_code": 1,
		"stdout":    strings.Repeat("stdout-line\n", 120),
		"stderr":    strings.Repeat("stderr-line\n", 120),
	})
	if err != nil {
		t.Fatal(err)
	}
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{
			CallID:  "call-1",
			Content: message.NewTextContent(string(full)),
		}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	out := res.Content.Text()
	if got := len([]rune(out)); got > 400 {
		t.Fatalf("truncated result = %d runes, want <= 400", got)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("truncated result is not valid JSON: %v\n%s", err, out)
	}
	if got := envelope["exit_code"]; got != float64(1) {
		t.Fatalf("exit_code = %v, want 1", got)
	}
	for _, key := range []string{"stdout", "stderr"} {
		excerpt, _ := envelope[key].(string)
		if !strings.Contains(excerpt, "truncated; full output:") {
			t.Fatalf("%s was not excerpted: %q", key, excerpt)
		}
	}
}

// A JSON value whose size comes from structure instead of a dominant
// string (a long match array) cannot be shrunk in place. It must still
// leave a valid JSON result, so it becomes a pointer envelope.
func TestTruncateMiddlewareBoundsStructuredJSONWithPointerEnvelope(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, ".opencraft", "cache", "tools")
	mw := truncateMiddleware(TruncateSettings{
		Enabled:  true,
		MaxChars: 300,
		Dir:      dir,
		WorkDir:  work,
	})
	matches := make([]map[string]any, 200)
	for i := range matches {
		matches[i] = map[string]any{"path": "src/file.go", "line": i}
	}
	full, err := json.Marshal(map[string]any{
		"matches":   matches,
		"truncated": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := func(context.Context, message.ToolCall) message.ToolResult {
		return message.ToolResult{
			CallID:  "call-1",
			Content: message.NewTextContent(string(full)),
		}
	}
	res := mw(next)(context.Background(), message.ToolCall{})
	out := res.Content.Text()
	if got := len([]rune(out)); got > 300 {
		t.Fatalf("truncated result = %d runes, want <= 300", got)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("truncated result is not valid JSON: %v\n%s", err, out)
	}
	if got := envelope["truncated"]; got != true {
		t.Fatalf("truncated = %v, want true", got)
	}
	fullOutput, _ := envelope["full_output"].(string)
	if !strings.Contains(fullOutput, "call-1.output") {
		t.Fatalf("full_output = %q, want the persisted pointer", fullOutput)
	}
	preview, _ := envelope["preview"].(string)
	if !strings.Contains(preview, "matches") ||
		!strings.Contains(preview, "truncated; full output:") {
		t.Fatalf("preview missing original text or marker: %q", preview)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "call-1.output"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(full) {
		t.Fatal("persisted output must stay byte-identical to the full result")
	}
}

// The plain-text excerpt is sliced at UTF-8 boundaries by hand, so a
// multi-byte value must neither split a rune nor change the rune count.
func TestHeadTailStringKeepsUTF8Boundaries(t *testing.T) {
	raw := strings.Repeat("中文🙂", 100)
	marker := []rune("\n…[marker]")
	got := headTailString(raw, 10, marker)
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt is not valid UTF-8: %q", got)
	}
	if want := 10 + len(marker); utf8.RuneCountInString(got) != want {
		t.Fatalf("excerpt = %d runes, want %d",
			utf8.RuneCountInString(got), want)
	}
	if !strings.HasPrefix(got, "中文🙂") || !strings.HasSuffix(got, "中文🙂") {
		t.Fatalf("excerpt lost its multi-byte head or tail: %q", got)
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
		return message.ToolResult{CallID: "call-1", Content: message.NewTextContent(strings.Repeat("x", 100))}
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
