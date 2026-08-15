package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/opencraft/internal/tui/commands"
)

// paletteState is the inline slash command palette. It opens the
// moment the input starts with "/" and re-ranks its results with BM25
// on every keystroke, so the user can type "/res" and see /resume.
type paletteState struct {
	open    bool
	query   string
	results []string // command names, BM25-ranked
	cursor  int
}

func (p *paletteState) reset() {
	p.open = false
	p.query = ""
	p.results = nil
	p.cursor = 0
}

// paletteOpen reports whether the palette is visible: idle mode with a
// single-line input starting with "/".
func (m *Model) paletteOpen() bool {
	text := strings.TrimSpace(m.input.Value())
	return m.mode == modeIdle &&
		strings.HasPrefix(text, "/") &&
		!strings.Contains(text, "\n")
}

// refreshPalette re-runs the BM25 search against the current
// "/query", or closes the palette when the input no longer looks like
// a command.
func (m *Model) refreshPalette() {
	if !m.paletteOpen() {
		m.palette.reset()
		return
	}
	text := strings.TrimSpace(m.input.Value())
	query := strings.TrimSpace(strings.TrimPrefix(text, "/"))
	m.palette.open = true
	m.palette.query = query
	results := m.commandIndex.Search(query, 8)
	m.palette.results = make([]string, 0, len(results))
	for _, r := range results {
		m.palette.results = append(m.palette.results, r.Name)
	}
	// A changed query re-ranks everything; start from the top.
	m.palette.cursor = 0
}

// paletteSelection returns the command Enter should run: the exact
// typed command when it matches, otherwise the highlighted BM25
// result, or "" when nothing matched (the text is then submitted as a
// normal message).
func (m *Model) paletteSelection() string {
	text := strings.TrimSpace(m.input.Value())
	name := strings.TrimSpace(strings.TrimPrefix(text, "/"))
	if _, ok := commands.Lookup(name); ok {
		return name
	}
	if m.paletteOpen() && len(m.palette.results) > 0 {
		idx := m.palette.cursor
		if idx >= len(m.palette.results) {
			idx = 0
		}
		return m.palette.results[idx]
	}
	return ""
}

// paletteView renders the inline command list above the composer.
func (m *Model) paletteView() string {
	rows := []string{reasoningLabelStyle.Render("? Commands")}
	if len(m.palette.results) == 0 {
		rows = append(rows, dimStyle.Render("  no matching commands"))
	} else {
		nameW := 0
		for _, name := range m.palette.results {
			nameW = max(nameW, len(name))
		}
		for i, name := range m.palette.results {
			desc := ""
			if c, ok := commands.Lookup(name); ok {
				desc = c.Desc
			}
			body := "/" + name +
				strings.Repeat(" ", nameW-len(name)+1) + desc
			if i == m.palette.cursor {
				rows = append(rows, userStyle.Render("❯ "+body))
			} else {
				rows = append(rows, dimStyle.Render("  "+body))
			}
		}
	}
	rows = append(rows,
		dimStyle.Render("  ↑/↓ choose · Enter run · Tab complete · Esc cancel"))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
