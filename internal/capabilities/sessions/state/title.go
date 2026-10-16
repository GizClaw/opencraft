package state

import (
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
)

// FirstTitle derives a conversation's fallback title from its archived
// messages: the first line a person wrote, or "[attachment]" when their
// first message carries only non-text parts. One rule serves every
// writer — the live store (Store.appendTurn), the JSON import
// (sessions.ImportJSON) and the legacy adopt (the compatibility
// importer) — so the same transcript is named the same way whichever
// path read it.
func FirstTitle(msgs []message.Message) string {
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		if title := firstTitleLine(m.Content.Text()); title != "" {
			return title
		}
		for _, part := range m.Content.Parts {
			if _, ok := part.(message.TextPart); !ok {
				return "[attachment]"
			}
		}
	}
	return ""
}

// firstTitleLine trims a message to its first line and caps the length,
// so a pasted document names the conversation by its opening words
// rather than by the whole paste.
func firstTitleLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const maxTitle = 70
	runes := []rune(s)
	if len(runes) > maxTitle {
		return string(runes[:maxTitle]) + "…"
	}
	return s
}
