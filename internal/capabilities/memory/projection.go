package memory

import (
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// The model window is a projection of the conversation transcript: one
// rule set, applied on read, decides which archived rows the model sees
// and how each one renders. It lives in this file so the archive write
// path (which renders a turn before it is stored) and the transcript read
// path (which projects stored rows back into the window) cannot drift
// apart — the rules are stated once, and
// TestProjectionMatchesStoredMemoryRows pins the result against what the
// previous build kept in memory_items.
//
// During increment A the projection deliberately reproduces the row set
// the memory write path accepted, so switching the read path changes no
// window:
//
//  1. rows of a turn the app authored are not conversation — a delegated
//     worker's note is transcript-only until B decides whether the model
//     reads it (the adapter's row query drops those turns);
//  2. an imported conversation's own system prompt stays in the archive
//     (the same query);
//  3. a row that renders to nothing — no text, no tool activity — is
//     skipped.
//
// Increment B retires rule 1 and turns rule 3 into a counted, warned
// skip.

// projectMessage returns the prompt projection of one stored message:
// canonical parts, plus the rendered tool activity as a text part when the
// message carries any. Tool calls and results are structured parts the
// provider needs, and the appended text is what keeps them readable in a
// projection that speaks text. The write path and the read path both go
// through this function, so a message cannot be stored in one shape and
// read back in another.
func projectMessage(m message.Message) *message.Message {
	rendered := m.Clone()
	if len(summarytext.ToolActivity(m)) == 0 {
		return &rendered
	}
	rendered.Content.Parts = append(rendered.Content.Parts,
		message.TextPart{Text: summarytext.RenderMessage(m)})
	return &rendered
}

// projectRow lowers one archived row into its model-facing message. It
// reports false for a row that projects to nothing (an image-only turn, a
// payload that carried only empty or non-text parts): such a row has no
// prompt form, and the window counts replayable rows only, so skipping it
// neither shrinks the window nor shifts the rows around it.
func projectRow(
	role message.Role,
	content message.Content,
) (message.Message, bool) {
	projected := projectMessage(message.Message{Role: role, Content: content})
	if projected.Content.Text() == "" {
		return message.Message{}, false
	}
	return *projected, true
}
