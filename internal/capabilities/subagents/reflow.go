package subagents

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
)

// maxNoteBytes bounds the delegation output quoted into the note the
// parent conversation receives. The full answer stays on the card (and
// is readable through delegation_status); the note exists so the parent
// learns the delegation finished without polling, not to duplicate a
// possibly large report into every later turn's window.
const maxNoteBytes = 4096

// Result is one finished delegation worth routing home: the terminal
// card, reduced to what the parent conversation needs.
type Result struct {
	// CardID identifies the delegation on the board. It is the
	// idempotency anchor — one card produces at most one note.
	CardID string
	// Target is the delegation target name (the subagent).
	Target string
	// RunID is the delegated run's id, empty when the worker never
	// recorded one.
	RunID string
	// Status is the terminal delegation status.
	Status delegation.Status
	// Output is the subagent's final answer, empty on failure.
	Output string
	// Error is the failure text, empty on success.
	Error string
	// ParentRunID is the delegating run, empty when unknown.
	ParentRunID string
	// ConversationID is the conversation the delegation's stream was
	// bound to. It is the only durable statement of where the result
	// belongs: it comes from the target the app itself exported at
	// submit time, not from anything the backend could invent.
	ConversationID string
	// At is when the event was observed. Board events carry no
	// timestamp of their own, and the note is written immediately, so
	// the receipt time is the closest honest answer to "when did this
	// finish".
	At time.Time
}

// ParseCardEvent reduces one board event to a result, reporting false
// for every event that is not a terminal delegation bound to a
// conversation.
//
// The stream target is required on purpose. A delegation the app never
// bound to a conversation has no home to deliver to, and the target is
// also the proof that a conversation existed when the work was
// submitted — a backend cannot mint a conversation by naming one.
func ParseCardEvent(ev kanban.CardEvent) (Result, bool) {
	if ev.Request == nil || ev.Request.Stream == nil {
		return Result{}, false
	}
	target := ev.Request.Stream.Target
	if target == nil ||
		target.Kind != delegation.StreamTargetKindConversation {
		return Result{}, false
	}
	conversationID := strings.TrimSpace(target.ID)
	if conversationID == "" {
		return Result{}, false
	}
	// The board's own status is the fallback, and only for the cards
	// that carry no response at all: a response that never turned
	// terminal is not a result either.
	status := delegation.Status("")
	output, errText := "", ""
	if ev.Response != nil {
		status = ev.Response.Status
		output, errText = ev.Response.Output, ev.Response.Error
	}
	if !status.Terminal() {
		status = statusFromBoard(ev.Status)
		if !status.Terminal() {
			return Result{}, false
		}
	}
	return Result{
		CardID:         ev.CardID,
		Target:         ev.Consumer,
		RunID:          ev.RunID,
		Status:         status,
		Output:         output,
		Error:          errText,
		ParentRunID:    ev.Request.ParentRunID,
		ConversationID: conversationID,
		At:             time.Now().UTC(),
	}, true
}

// statusFromBoard maps a board status onto the delegation vocabulary.
func statusFromBoard(status kanban.Status) delegation.Status {
	switch status {
	case kanban.StatusDone:
		return delegation.StatusSucceeded
	case kanban.StatusFailed:
		return delegation.StatusFailed
	case kanban.StatusCanceled:
		return delegation.StatusCanceled
	}
	return ""
}

// Key is the note's stable identity: one card, one note, no matter how
// often the event is redelivered (a restart re-reads the board, and a
// backend may deliver the same terminal event twice).
func (r Result) Key() string {
	return "subagent:" + r.CardID
}

// Note renders the message the parent conversation receives. The
// header names the subagent and the outcome; the body quotes the
// answer (or the failure). The wording matters: this row lands in the
// archive as a user-role message, which is how the app speaks to the
// model outside a turn, so it has to read as a report rather than as
// something the user said.
func (r Result) Note() string {
	target := r.Target
	if target == "" {
		target = "subagent"
	}
	var b strings.Builder
	// This text becomes the first user message of the note's turn, so
	// keep the header on one line: the store derives empty titles
	// from a first line.
	fmt.Fprintf(&b, "[delegated worker %q finished: %s]", target, r.Status)
	if ref := r.reference(); ref != "" {
		b.WriteString("\n")
		b.WriteString(ref)
	}
	switch {
	case r.Status == delegation.StatusSucceeded:
		if body := strings.TrimSpace(r.Output); body != "" {
			b.WriteString("\n\n")
			b.WriteString(excerpt(body, maxNoteBytes))
		}
	case strings.TrimSpace(r.Error) != "":
		b.WriteString("\n\n")
		b.WriteString(excerpt(strings.TrimSpace(r.Error), maxNoteBytes))
	}
	return b.String()
}

// reference names the delegation so a reader can find the card again.
func (r Result) reference() string {
	parts := make([]string, 0, 3)
	if r.CardID != "" {
		parts = append(parts, "card "+r.CardID)
	}
	if r.RunID != "" {
		parts = append(parts, "run "+r.RunID)
	}
	if r.ParentRunID != "" {
		parts = append(parts, "asked by run "+r.ParentRunID)
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// excerpt bounds text to limit bytes without splitting a rune.
func excerpt(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := text[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n[truncated]"
}
