package worldstate

import "strings"

// The per-turn context block. Go renders it into one string
// (world.tail_block) and the world node appends it to the user's own
// message as a final text part.
//
// Two design rules are encoded here:
//
//   - The block rides with the turn's message instead of becoming
//     messages of its own. Provider prompt caches only reuse the longest
//     common prefix of the previous request, so a message that changes
//     between turns sits in the middle of the conversation and turns
//     every byte behind it into a cache miss. The user's message is the
//     one position that is expected to differ every turn.
//   - The block is framed as injected context. The model must not read
//     the plan or the skills list as the user's words, and the user's
//     request stays the text before the block.
const (
	tailBlockHeader = "<opencraft-context>\n" +
		"Injected context for this turn. It is not part of the user's " +
		"message: the user's request is the text before this block.\n\n"
	tailBlockFooter = "\n</opencraft-context>"
)

// renderTailBlock renders the per-turn sections into the single text
// block the world node appends to the user's message. Empty sections are
// skipped, and a fully empty tail returns "" so the node leaves the turn
// message untouched.
func renderTailBlock(sections []Section) string {
	var b strings.Builder
	for _, sec := range sections {
		text := strings.TrimSpace(sec.Content.Text())
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)
	}
	if b.Len() == 0 {
		return ""
	}
	return tailBlockHeader + b.String() + tailBlockFooter
}
