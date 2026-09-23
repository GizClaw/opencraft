package bindings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

func TestProcessViewShape(t *testing.T) {
	started := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	code := 3
	view := toProcessView(sandbox.Process{
		ID:             "p-1",
		ConversationID: "s-1",
		Argv:           []string{"npm", "run", "build"},
		Workdir:        "/ws",
		TTY:            true,
		PID:            99,
		StartedAt:      started,
		Running:        false,
		ExitCode:       &code,
		ExitReason:     "exited",
		Tail:           "built\n",
		Truncated:      true,
		Seq:            6,
	})
	if view.ProcessID != "p-1" || view.PID != 99 || view.TTY != true {
		t.Fatalf("identity fields = %+v", view)
	}
	if view.StartedAt != "2026-09-04T12:00:00Z" {
		t.Fatalf("started_at = %q, want RFC3339 UTC", view.StartedAt)
	}
	if view.ExitCode == nil || *view.ExitCode != 3 || view.Running {
		t.Fatalf("exit fields = %+v", view)
	}
	if !view.Truncated || view.Seq != 6 || view.Tail != "built\n" {
		t.Fatalf("tail fields = %+v", view)
	}
	// The DTO carries its own argv slice: mutating it must not reach
	// back into the feed's snapshot.
	view.Argv[0] = "changed"
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"process_id":"p-1"`) ||
		!strings.Contains(string(raw), `"exit_code":3`) {
		t.Fatalf("process view json = %s", raw)
	}
}

func TestSessionMetaJSONShape(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	dto := toSessionMeta(sessions.Meta{
		ID:        "s-1",
		Title:     "hello",
		CreatedAt: now,
		UpdatedAt: now,
		Turns:     2,
		Messages:  2,
		Usage:     sessions.Usage{TotalTokens: 42},
	})
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"id", "title", "created_at", "updated_at", "turns", "messages",
		"total_tokens",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("session meta JSON missing %q: %s", key, raw)
		}
	}
	if got["total_tokens"].(float64) != 42 {
		t.Fatalf("total_tokens = %v, want 42", got["total_tokens"])
	}
}

func TestSessionTurnDTOFallsBackToTurnTime(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	started := now.Add(-90 * time.Second)
	dto := toSessionTurnDTO(context.Background(), "s-1", sessions.TurnRecord{
		Seq:        3,
		At:         now,
		StartedAt:  started,
		FinishedAt: now,
		RunID:      "run-1",
		Status:     "failed",
		Error:      "boom",
		Artifacts: []sessions.Artifact{{
			Path:  "a.txt",
			Bytes: 3,
		}},
	})
	if dto.StartedAt != started.Format(time.RFC3339) ||
		dto.FinishedAt != now.Format(time.RFC3339) {
		t.Fatalf("turn times = %+v", dto)
	}
	if dto.DurationMs != 90_000 {
		t.Fatalf("duration_ms = %d, want 90000", dto.DurationMs)
	}
	if dto.RequestedAt != now.Format(time.RFC3339) {
		t.Fatalf("requested_at fallback = %q", dto.RequestedAt)
	}
	if dto.Status != "failed" || dto.Error != "boom" {
		t.Fatalf("status/error = %q/%q", dto.Status, dto.Error)
	}
}

func TestRequireArchivedTurnsRejectsEmpty(t *testing.T) {
	if err := requireArchivedTurns([]sessions.TurnRecord{{Seq: 1}}); err != nil {
		t.Fatalf("non-empty turns rejected: %v", err)
	}
	if err := requireArchivedTurns(nil); err == nil {
		t.Fatal("empty turns accepted for export")
	}
}

// TestSessionTurnDTOFilesDelegationNote pins the read side of the note
// contract: a turn the app wrote arrives as a turn whose fields are
// already decoded, an unknown kind travels through without being
// guessed at, and a payload this build cannot read degrades to the
// row's text rather than failing the read.
func TestSessionTurnDTOFilesDelegationNote(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	payload, err := subagents.NotePayload{
		Target:      "researcher",
		Status:      "succeeded",
		CardID:      "card-1",
		RunID:       "run-child",
		ParentRunID: "run-parent",
		Body:        "the report",
	}.Encode()
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	dto := toSessionTurnDTO(context.Background(), "s-1", sessions.TurnRecord{
		Seq:     4,
		At:      now,
		RunID:   "subagent:card-1",
		Kind:    subagents.KindDelegationNote,
		Payload: payload,
	})
	if dto.Kind != subagents.KindDelegationNote || dto.Note == nil {
		t.Fatalf("note turn = %+v", dto)
	}
	if dto.Note.Target != "researcher" || dto.Note.Status != "succeeded" ||
		dto.Note.CardID != "card-1" || dto.Note.RunID != "run-child" ||
		dto.Note.ParentRunID != "run-parent" ||
		dto.Note.Body != "the report" {
		t.Fatalf("note = %+v", dto.Note)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"kind":"delegation_note"`, `"delegation_note":{`,
		`"parent_run_id":"run-parent"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("note turn JSON missing %s: %s", key, raw)
		}
	}

	// An ordinary turn carries no note, and a kind from a newer build
	// is passed through untouched instead of being decoded as one this
	// build knows.
	if plain := toSessionTurnDTO(context.Background(), "s-1",
		sessions.TurnRecord{Seq: 5, At: now}); plain.Kind != "" ||
		plain.Note != nil {
		t.Fatalf("plain turn = %+v", plain)
	}
	future := toSessionTurnDTO(context.Background(), "s-1",
		sessions.TurnRecord{
			Seq:     6,
			At:      now,
			Kind:    "something_newer",
			Payload: payload,
		})
	if future.Kind != "something_newer" || future.Note != nil {
		t.Fatalf("future turn = %+v", future)
	}

	// A note whose payload does not decode keeps its kind and renders
	// from the message text: losing the card loses nothing.
	broken := toSessionTurnDTO(context.Background(), "s-1",
		sessions.TurnRecord{
			Seq:     7,
			At:      now,
			Kind:    subagents.KindDelegationNote,
			Payload: []byte("{not json"),
		})
	if broken.Kind != subagents.KindDelegationNote || broken.Note != nil {
		t.Fatalf("broken note turn = %+v", broken)
	}
}

// TestDelegationNoteHeadingKeepsTheAuthorsName pins the exported
// markdown's label for a note turn: the app's report is never filed
// under the user's heading.
func TestDelegationNoteHeadingKeepsTheAuthorsName(t *testing.T) {
	payload, err := subagents.NotePayload{
		Target: "researcher",
		Status: "succeeded",
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	heading := delegationNoteHeading(context.Background(), "s-1",
		sessions.TurnRecord{Kind: subagents.KindDelegationNote, Payload: payload})
	if heading != "Delegated result: researcher (succeeded)" {
		t.Fatalf("heading = %q", heading)
	}
	// A note whose fields are missing is still the app reporting, not
	// the user speaking.
	if heading := delegationNoteHeading(context.Background(), "s-1",
		sessions.TurnRecord{Kind: subagents.KindDelegationNote}); heading != "Delegated result" {
		t.Fatalf("heading without payload = %q", heading)
	}
}

func TestImportRequestFromTurnsCarriesTimingAndUsage(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	requested := at.Add(-90 * time.Second)
	started := at.Add(-85 * time.Second)
	usage := sessions.Usage{
		Model:           "deepseek-v4-flash",
		InputTokens:     1000,
		OutputTokens:    200,
		TotalTokens:     1200,
		CacheReadTokens: 700,
		ReasoningTokens: 50,
	}
	turns := []sessions.TurnRecord{
		{
			At:          at,
			RequestedAt: requested,
			StartedAt:   started,
			FinishedAt:  at,
		},
		{At: at.Add(5 * time.Minute)},
	}

	req := importRequestFromTurns("opencraft:s-1", "s-1", usage, turns)
	if req.Source != "opencraft:s-1" || req.Title != "s-1" {
		t.Fatalf("source/title = %q/%q", req.Source, req.Title)
	}
	if req.Usage == nil || *req.Usage != usage {
		t.Fatalf("usage = %+v, want %+v", req.Usage, usage)
	}
	if len(req.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(req.Turns))
	}
	first := req.Turns[0]
	if first.RequestedAt == nil || !first.RequestedAt.Equal(requested) ||
		first.StartedAt == nil || !first.StartedAt.Equal(started) ||
		first.FinishedAt == nil || !first.FinishedAt.Equal(at) {
		t.Fatalf("turn 1 timestamps = %+v", first)
	}
	second := req.Turns[1]
	if second.RequestedAt != nil || second.StartedAt != nil ||
		second.FinishedAt != nil {
		t.Fatalf("legacy turn should omit optional timestamps: %+v", second)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"usage", "requested_at", "started_at", "finished_at",
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("export JSON missing %q: %s", key, raw)
		}
	}
}
