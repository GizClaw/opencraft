package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// markdownStyle is a curated glamour style for assistant output:
// white bold headings, cyan underlined links, dim italic quotes and
// code, and check-marked task lists. It keeps the terminal default
// foreground for body text and only uses semantic ANSI colors, so
// rendered output stays within the app palette instead of pulling in
// a 256-color or RGB theme.
const markdownStyle = `{
  "document": {"block_prefix": "\n", "block_suffix": "\n", "margin": 0},
  "block_quote": {"indent": 1, "indent_token": "│ ", "color": "8", "italic": true},
  "paragraph": {},
  "list": {"level_indent": 4},
  "heading": {"block_suffix": "\n", "bold": true, "color": "15"},
  "h1": {"prefix": "# "},
  "h2": {"prefix": "## "},
  "h3": {"prefix": "### "},
  "h4": {"prefix": "#### "},
  "h5": {"prefix": "##### "},
  "h6": {"prefix": "###### "},
  "text": {},
  "strikethrough": {"block_prefix": "~~", "block_suffix": "~~"},
  "emph": {"italic": true, "color": "8"},
  "strong": {"bold": true},
  "hr": {"color": "8", "format": "\n────────\n"},
  "item": {"block_prefix": "• "},
  "enumeration": {"block_prefix": ". "},
  "task": {"ticked": "[✓] ", "unticked": "[ ] "},
  "link": {"underline": true, "color": "14"},
  "link_text": {"bold": true},
  "code": {"block_prefix": "\u0060", "block_suffix": "\u0060", "color": "8"},
  "code_block": {"margin": 2, "color": "8"},
  "table": {},
  "definition_list": {},
  "definition_term": {"bold": true},
  "definition_description": {"block_prefix": "\n  "}
}`

// markdownRenderer renders assistant paragraphs with the fixed style
// above. A fixed style never queries the terminal background, so its
// responses cannot leak into the UI's keyboard stream.
var markdownRenderer = func() *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes([]byte(markdownStyle)),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		panic("tui: create markdown renderer: " + err.Error())
	}
	return r
}()

// renderMarkdown renders one paragraph and returns its non-empty
// lines. A rendering failure falls back to the raw text.
func renderMarkdown(paragraph string) []string {
	rendered, err := markdownRenderer.Render(paragraph)
	if err != nil || strings.TrimSpace(rendered) == "" {
		return strings.Split(strings.TrimRight(paragraph, "\n"), "\n")
	}
	var lines []string
	for _, l := range strings.Split(rendered, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return frameTable(lines)
}

// frameTable draws an outer box around a glamour-rendered markdown
// table. glamour hardcodes the table's outer borders off (it only
// keeps the header separator and column separators), so the frame is
// added here: ┌─┐ on top, ├─┤ around the separator row and └─┘ at
// the bottom, with every content row wrapped in │ … │.
func frameTable(lines []string) []string {
	if !isTableBlock(lines) {
		return lines
	}
	width := 0
	for _, l := range lines {
		width = max(width, lipgloss.Width(l))
	}
	if width == 0 {
		return lines
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, "┌"+strings.Repeat("─", width)+"┐")
	for i, l := range lines {
		pad := strings.Repeat(" ", width-lipgloss.Width(l))
		if i == 1 {
			// The header separator row becomes the box's mid line.
			out = append(out, "├"+l+pad+"┤")
			continue
		}
		out = append(out, "│"+l+pad+"│")
	}
	return append(out, "└"+strings.Repeat("─", width)+"┘")
}

// isTableBlock reports whether glamour-rendered lines are a table: a
// separator row built from ─/┼ box-drawing characters plus at least
// one column-separated row.
func isTableBlock(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	hasColumns := false
	for i, l := range lines {
		if i == 1 {
			if l == "" {
				return false
			}
			for _, r := range l {
				if r != '─' && r != '┼' {
					return false
				}
			}
			continue
		}
		if strings.ContainsRune(l, '│') {
			hasColumns = true
		}
	}
	return hasColumns
}
