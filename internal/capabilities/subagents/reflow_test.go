package subagents

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
)

func conversationEvent(
	status kanban.Status, response *delegation.Response,
) kanban.CardEvent {
	return kanban.CardEvent{
		CardID:   "card-1",
		Consumer: "researcher",
		RunID:    "run-child",
		Status:   status,
		Request: &delegation.AsyncRequest{
			ParentRunID: "run-parent",
			Request: delegation.Request{
				Mode:   delegation.ModeAsync,
				Target: "researcher",
				Input:  "dig",
			},
			Stream: &delegation.StreamRef{Target: &delegation.StreamTarget{
				Kind: delegation.StreamTargetKindConversation,
				ID:   "s-1",
			}},
		},
		Response: response,
	}
}

// TestParseCardEventAcceptsTerminalBoundCards is the happy path, and
// the source of every field the note prints.
func TestParseCardEventAcceptsTerminalBoundCards(t *testing.T) {
	before := time.Now().UTC()
	got, ok := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{
			ID:     "card-1",
			Status: delegation.StatusSucceeded,
			Output: "  the report  ",
		}))
	if !ok {
		t.Fatal("terminal conversation-bound card was not accepted")
	}
	if got.CardID != "card-1" || got.Target != "researcher" ||
		got.RunID != "run-child" || got.ParentRunID != "run-parent" ||
		got.ConversationID != "s-1" ||
		got.Status != delegation.StatusSucceeded {
		t.Fatalf("result = %+v", got)
	}
	if got.At.Before(before) || got.At.After(time.Now().UTC()) {
		t.Fatalf("At = %v, want the receipt time", got.At)
	}

	// A board-only terminal status (the worker died before producing a
	// response) still routes: the parent has to learn the delegation
	// ended, or it waits forever.
	got, ok = ParseCardEvent(conversationEvent(kanban.StatusFailed, nil))
	if !ok || got.Status != delegation.StatusFailed {
		t.Fatalf("board-only failure: %+v ok=%v", got, ok)
	}
	got, ok = ParseCardEvent(conversationEvent(kanban.StatusCanceled, nil))
	if !ok || got.Status != delegation.StatusCanceled {
		t.Fatalf("board-only cancel: %+v ok=%v", got, ok)
	}
}

// TestParseCardEventRejectsUnroutableCards pins what must never produce
// a note: non-terminal cards, and cards with no conversation to deliver
// to (the target is the proof a conversation existed at submit time).
func TestParseCardEventRejectsUnroutableCards(t *testing.T) {
	cases := map[string]kanban.CardEvent{
		"no request": {CardID: "c", Status: kanban.StatusDone},
		"no stream": func() kanban.CardEvent {
			ev := conversationEvent(kanban.StatusDone, nil)
			ev.Request.Stream = nil
			return ev
		}(),
		"stream without target": func() kanban.CardEvent {
			ev := conversationEvent(kanban.StatusDone, nil)
			ev.Request.Stream.Target = nil
			return ev
		}(),
		"other target kind": func() kanban.CardEvent {
			ev := conversationEvent(kanban.StatusDone, nil)
			ev.Request.Stream.Target.Kind = "bus"
			return ev
		}(),
		"blank conversation id": func() kanban.CardEvent {
			ev := conversationEvent(kanban.StatusDone, nil)
			ev.Request.Stream.Target.ID = "   "
			return ev
		}(),
		"still pending": conversationEvent(kanban.StatusPending, nil),
		"still claimed": conversationEvent(kanban.StatusClaimed, nil),
		"non-terminal response": conversationEvent(kanban.StatusClaimed,
			&delegation.Response{ID: "c", Status: delegation.StatusRunning}),
	}
	for name, ev := range cases {
		if got, ok := ParseCardEvent(ev); ok {
			t.Errorf("%s: parsed %+v, want no result", name, got)
		}
	}
}

// TestResultKeyIsPerCard pins the dedupe anchor: redelivery of the same
// card (a restart re-reads the board) must map to the same note.
func TestResultKeyIsPerCard(t *testing.T) {
	first, _ := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{ID: "card-1", Status: delegation.StatusSucceeded}))
	second, _ := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{ID: "card-1", Status: delegation.StatusSucceeded}))
	if first.Key() != second.Key() {
		t.Fatalf("keys differ: %q vs %q", first.Key(), second.Key())
	}
	if !strings.Contains(first.Key(), "card-1") {
		t.Fatalf("key %q does not name the card", first.Key())
	}
	other := first
	other.CardID = "card-2"
	if first.Key() == other.Key() {
		t.Fatal("different cards share a key")
	}
}

// TestNoteReportsSuccessAndFailure pins the note's shape: the header
// names the subagent and the outcome on one line (the store derives
// empty titles from a first line), and the body carries the answer or
// the failure.
func TestNoteReportsSuccessAndFailure(t *testing.T) {
	done, _ := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{
			ID:     "card-1",
			Status: delegation.StatusSucceeded,
			Output: "\n  found three candidates  \n",
		}))
	note := done.Note()
	header, body, found := strings.Cut(note, "\n")
	if !found || strings.Contains(header, "\n") {
		t.Fatalf("note = %q, want a one-line header", note)
	}
	if !strings.Contains(header, "researcher") ||
		!strings.Contains(header, string(delegation.StatusSucceeded)) {
		t.Fatalf("header = %q", header)
	}
	if !strings.Contains(body, "found three candidates") ||
		!strings.Contains(body, "card-1") {
		t.Fatalf("body = %q, want the output and the card reference", body)
	}

	failed, _ := ParseCardEvent(conversationEvent(kanban.StatusFailed,
		&delegation.Response{
			ID:     "card-1",
			Status: delegation.StatusFailed,
			Error:  "provider rejected the request",
		}))
	note = failed.Note()
	if !strings.Contains(note, string(delegation.StatusFailed)) ||
		!strings.Contains(note, "provider rejected the request") {
		t.Fatalf("failure note = %q", note)
	}

	// A failure with no error text still says something: the status is
	// the minimum honest report.
	empty, _ := ParseCardEvent(conversationEvent(kanban.StatusCanceled, nil))
	if note := empty.Note(); !strings.Contains(note, string(delegation.StatusCanceled)) {
		t.Fatalf("cancel note = %q", note)
	}
	// A card with no target name falls back to a readable label rather
	// than rendering an empty quoted name.
	anon := empty
	anon.Target = ""
	if note := anon.Note(); !strings.Contains(note, "subagent") {
		t.Fatalf("anonymous note = %q", note)
	}
}

// TestNoteBoundsTheOutput keeps the note from duplicating a large
// report into the parent's window: the full answer stays on the card.
func TestNoteBoundsTheOutput(t *testing.T) {
	result, _ := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{ID: "card-1", Status: delegation.StatusSucceeded}))
	// Multi-byte runes throughout, so a byte-slicing excerpt would
	// produce an invalid tail.
	result.Output = strings.Repeat("报", maxNoteBytes)
	note := result.Note()
	if len(note) > maxNoteBytes+1024 {
		t.Fatalf("note is %d bytes, want it bounded", len(note))
	}
	if !strings.Contains(note, "[truncated]") {
		t.Fatalf("note does not mark the truncation: %q", note)
	}
	if !utf8.ValidString(note) {
		t.Fatal("note is not valid UTF-8: the excerpt split a rune")
	}
}

// TestNotePayloadWireShape pins the stored form of a note: the fields
// the transcript card reads, encoded exactly as the backfill step
// (foundation/compat, 018) reconstructs them from older archives. The
// same literal is asserted there in
// TestDelegationNoteBackfillParsesRenderedNotes, so the two writers —
// this one, and the migration parsing prose — cannot drift apart
// without a failing test on one of the paths.
func TestNotePayloadWireShape(t *testing.T) {
	result, _ := ParseCardEvent(conversationEvent(kanban.StatusDone,
		&delegation.Response{
			ID:     "card-1",
			Status: delegation.StatusSucceeded,
			Output: "the report",
		}))
	payload := result.Payload()
	if payload.Target != "researcher" || payload.RunID != "run-child" ||
		payload.ParentRunID != "run-parent" || payload.Body != "the report" {
		t.Fatalf("payload = %+v", payload)
	}
	raw, err := payload.Encode()
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"target":"researcher","status":"succeeded",` +
		`"card_id":"card-1","run_id":"run-child",` +
		`"parent_run_id":"run-parent","body":"the report"}`
	if string(raw) != want {
		t.Fatalf("payload = %s, want %s", raw, want)
	}

	// The note text is rendered from the same fields, so what the model
	// reads and what the card shows cannot disagree.
	note := result.Note()
	for _, part := range []string{
		payload.Target, payload.Status, payload.CardID,
		payload.RunID, payload.ParentRunID, payload.Body,
	} {
		if !strings.Contains(note, part) {
			t.Fatalf("note %q does not carry %q", note, part)
		}
	}

	// A failed delegation reports the error in the body: the card has to
	// show why, not an empty answer.
	failed, _ := ParseCardEvent(conversationEvent(kanban.StatusFailed,
		&delegation.Response{
			ID:     "card-1",
			Status: delegation.StatusFailed,
			Error:  "boom",
		}))
	if body := failed.Payload().Body; body != "boom" {
		t.Fatalf("failure payload body = %q, want the error text", body)
	}
	if body := failed.Payload().Body; !strings.Contains(failed.Note(), body) {
		t.Fatalf("failure note = %q, want the failing body", failed.Note())
	}
}
