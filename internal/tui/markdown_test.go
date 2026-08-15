package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownStyle(t *testing.T) {
	lines := renderMarkdown(
		"# 标题\n\n" +
			"这是 **粗体** 和 [链接](https://example.com)。\n\n" +
			"> 引用\n\n" +
			"- [x] 已完成\n" +
			"- [ ] 未完成\n",
	)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b[97;1m标题\x1b[0m") {
		t.Errorf("heading not bold white: %q", joined)
	}
	if !strings.Contains(joined, "\x1b[96;4mhttps://example.com\x1b[0m") {
		t.Errorf("link not cyan underlined: %q", joined)
	}
	if !strings.Contains(joined, "[✓] 已完成") ||
		!strings.Contains(joined, "[ ] 未完成") {
		t.Errorf("task list glyphs missing: %q", joined)
	}
}

func TestRenderMarkdownTableFramed(t *testing.T) {
	lines := renderMarkdown("| 语言 | 类型 |\n| --- | --- |\n| Go | 编译型 |\n")
	if len(lines) != 5 {
		t.Fatalf("framed table lines = %d, want 5: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "┌") || !strings.HasSuffix(lines[0], "┐") {
		t.Errorf("top border = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "│") || !strings.HasSuffix(lines[1], "│") {
		t.Errorf("header row = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "├") || !strings.HasSuffix(lines[2], "┤") {
		t.Errorf("separator row = %q", lines[2])
	}
	if !strings.HasPrefix(lines[4], "└") || !strings.HasSuffix(lines[4], "┘") {
		t.Errorf("bottom border = %q", lines[4])
	}
}

func TestRenderMarkdownPlainNotFramed(t *testing.T) {
	lines := renderMarkdown("这是普通段落，不是表格。")
	if len(lines) != 1 || strings.HasPrefix(lines[0], "┌") {
		t.Errorf("plain paragraph should stay unframed: %q", lines)
	}
}
