package memory

import (
	"strings"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// The model window is a projection of the conversation transcript: one
// rule set, applied on read, decides which archived rows the model sees
// and how each one renders. It lives in this file so the archive write
// path (which stores a turn) and the transcript read path (which projects
// stored rows back into the window) cannot drift apart — the rules are
// stated once, and the projection golden cases pin the result.
//
// The rules, in full:
//
//  1. every archived row of the conversation is a candidate, whoever
//     wrote it. A delegation note is the app reporting a worker's result
//     to the model, so it belongs in the window like any other user-role
//     row; the turn's `kind` tells a *reader* what the row is (the
//     transcript card, the title fallback), it does not keep the row out
//     of the model's context.
//  2. an imported conversation's own system prompt stays in the archive.
//     It describes the source application's environment, which
//     contradicts OpenCraft's world-state sections, so the transcript
//     query never selects `role = system` rows.
//  3. a row that carries no text of its own — an image-only turn, an
//     attachment whose kind has no text form — enters the window as a
//     placeholder naming what it does carry, and the read path counts it
//     (see loadProjectedBefore). Rendering nothing would drop a turn the
//     user authored; inventing its content would be worse.
//  4. a payload with no honest rendering — one that does not decode as
//     canonical content, or whose parts are all traces the prompt never
//     carries (a signature-only reasoning part) — is skipped: counted and
//     warned, never silently.
//
// Rules 1 and 4 are what changed when the second copy of the history
// (memory_items) was retired: a note is now visible to the model, and a
// row that has no prompt form is reported rather than lost.

// placeholderFor names what a text-less row carries, so the model sees
// that something was there instead of a hole in the conversation.
func placeholderFor(content message.Content) string {
	kinds := make([]string, 0, len(content.Parts))
	seen := make(map[string]bool, len(content.Parts))
	add := func(kind string) {
		if kind == "" || seen[kind] {
			return
		}
		seen[kind] = true
		kinds = append(kinds, kind)
	}
	for _, part := range content.Parts {
		normalized, err := message.NormalizePart(part)
		if err != nil {
			continue
		}
		switch normalized.(type) {
		case message.TextPart, message.ReasoningPart:
			// Empty text: nothing to say, and nothing to name.
		case message.ImagePart:
			add("image")
		case message.AudioPart:
			add("audio")
		case message.VideoPart:
			add("video")
		case message.FilePart:
			add("file")
		case message.DataPart:
			add("data")
		case message.ToolCallPart, message.ToolResultPart:
			add("tool")
		default:
			add("unknown")
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	return "[attachment: " + strings.Join(kinds, ", ") + "]"
}

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

// projection is one row's model-facing form plus what the read path had
// to do to get there.
type projection struct {
	Message message.Message
	// Placeholder is true when the row carried no text of its own and the
	// window says so instead. The read path counts these: a conversation
	// that renders as placeholders is a conversation the model cannot
	// read, and that is worth knowing.
	Placeholder bool
}

// projectRow lowers one archived row into its model-facing message.
//
// ok reports whether the row has a prompt form at all: a payload with no
// text of its own renders as a placeholder, while a payload with nothing
// to name — every part a trace the prompt never carries — is skipped. The
// window counts replayable rows only, so skipping such a row neither
// shrinks the window in a way the caller cannot see (the caller counts it)
// nor shifts the rows around it.
func projectRow(
	role message.Role,
	content message.Content,
) (projection, bool) {
	projected := projectMessage(message.Message{Role: role, Content: content})
	if text := projected.Content.Text(); text != "" {
		return projection{Message: *projected}, true
	}
	placeholder := placeholderFor(content)
	if placeholder == "" {
		return projection{}, false
	}
	stub := message.Message{Role: role, Content: message.Content{
		Parts: []message.Part{message.TextPart{Text: placeholder}},
	}}
	return projection{Message: stub, Placeholder: true}, true
}
