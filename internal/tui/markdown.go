package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// markdownStyle is a minimal glamour style: the terminal default
// foreground with bold headings/strong text, italic emphasis, and dim
// inline/fenced code. It skips the dark style's 256-color palette and
// code syntax highlighting so the assistant output stays within the
// semantic ANSI palette.
const markdownStyle = `{
  "document": {"block_prefix": "\n", "block_suffix": "\n", "margin": 0},
  "block_quote": {"indent": 1, "indent_token": "│ ", "color": "8"},
  "paragraph": {},
  "list": {"level_indent": 4},
  "heading": {"block_suffix": "\n", "bold": true},
  "h1": {"prefix": "# ", "block_suffix": "\n"},
  "h2": {"prefix": "## ", "block_suffix": "\n"},
  "h3": {"prefix": "### ", "block_suffix": "\n"},
  "h4": {"prefix": "#### ", "block_suffix": "\n"},
  "h5": {"prefix": "##### ", "block_suffix": "\n"},
  "h6": {"prefix": "###### ", "block_suffix": "\n"},
  "text": {},
  "strikethrough": {"block_prefix": "~~", "block_suffix": "~~"},
  "emph": {"italic": true},
  "strong": {"bold": true},
  "hr": {"format": "\n--------\n"},
  "item": {"block_prefix": "• "},
  "enumeration": {"block_prefix": ". "},
  "task": {"ticked": "[x] ", "unticked": "[ ] "},
  "link": {"underline": true},
  "link_text": {},
  "code": {"block_prefix": "\u0060", "block_suffix": "\u0060", "color": "8"},
  "code_block": {"margin": 2, "color": "8"},
  "table": {},
  "definition_list": {},
  "definition_term": {},
  "definition_description": {"block_prefix": "\n* "}
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
	return lines
}
