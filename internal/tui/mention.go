package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/opencraft/internal/skills"
)

// mentionState is the inline $skill completion popup. It opens when
// the idle composer's last token starts with "$" and re-ranks on
// every keystroke; selecting one completes the token to "$name ".
// The backend stays the only injection owner — completion never
// expands skill content into the message.
type mentionState struct {
	open    bool
	prefix  string
	results []skills.SkillMetadata
	cursor  int
}

func (m *mentionState) reset() {
	m.open = false
	m.prefix = ""
	m.results = nil
	m.cursor = 0
}

var mentionTokenRe = regexp.MustCompile(`\$[a-z0-9-]*$`)

// mentionActive reports whether the composer currently requests a
// completion: idle mode, single line, last token starting with "$".
func (m *Model) mentionActive() bool {
	if m.mode != modeIdle || m.opts.Skills == nil || !m.opts.Skills.Enabled() {
		return false
	}
	text := m.input.Value()
	if text == "" || strings.Contains(text, "\n") ||
		strings.HasPrefix(strings.TrimSpace(text), "/") {
		return false
	}
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return false
	}
	return strings.HasPrefix(tokens[len(tokens)-1], "$")
}

// mentionOpen reports whether the completion popup is visible.
func (m *Model) mentionOpen() bool {
	return m.mentionActive() && m.mention.open
}

// mentionPrefix returns the text after the trailing "$".
func (m *Model) mentionPrefix() string {
	tokens := strings.Fields(m.input.Value())
	if len(tokens) == 0 {
		return ""
	}
	return strings.TrimPrefix(tokens[len(tokens)-1], "$")
}

// refreshMention re-ranks skills against the trailing $prefix.
func (m *Model) refreshMention() {
	if !m.mentionActive() {
		m.mention.reset()
		return
	}
	prefix := m.mentionPrefix()
	results := m.opts.Skills.Rank(prefix, 8, 0)
	if len(results) == 0 {
		m.mention.reset()
		return
	}
	m.mention.open = true
	m.mention.prefix = prefix
	m.mention.results = results
	m.mention.cursor = 0
}

// mentionSelection returns the highlighted skill.
func (m *Model) mentionSelection() (skills.SkillMetadata, bool) {
	if !m.mention.open || len(m.mention.results) == 0 {
		return skills.SkillMetadata{}, false
	}
	idx := m.mention.cursor
	if idx >= len(m.mention.results) {
		idx = 0
	}
	return m.mention.results[idx], true
}

// completeMention replaces the trailing $token with "$name ".
func (m *Model) completeMention(sk skills.SkillMetadata) {
	text := m.input.Value()
	if loc := mentionTokenRe.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + "$" + sk.Name + " "
	} else {
		text += "$" + sk.Name + " "
	}
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.resizeInput()
}

// mentionView renders the completion list above the composer.
func (m *Model) mentionView() string {
	rows := []string{reasoningLabelStyle.Render("? Skills")}
	if len(m.mention.results) == 0 {
		rows = append(rows, dimStyle.Render("  no matching skills"))
	} else {
		nameW := 0
		for _, sk := range m.mention.results {
			nameW = max(nameW, len(sk.Name))
		}
		for i, sk := range m.mention.results {
			body := "$" + sk.Name +
				strings.Repeat(" ", nameW-len(sk.Name)+1) + sk.Description
			if i == m.mention.cursor {
				rows = append(rows, userStyle.Render("❯ "+body))
			} else {
				rows = append(rows, dimStyle.Render("  "+body))
			}
		}
	}
	rows = append(rows,
		dimStyle.Render("  ↑/↓ choose · Enter/Tab complete · Esc cancel"))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
