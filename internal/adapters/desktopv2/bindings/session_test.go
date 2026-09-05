package bindings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

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
	dto := toSessionTurnDTO(sessions.TurnRecord{
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

func TestDelegationCardDTOCarriesDetails(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	card := &kanban.Card{
		ID:        "card-1",
		Producer:  "producer",
		Consumer:  "consumer",
		Status:    kanban.StatusDone,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		Task: &kanban.Task{Request: delegation.AsyncRequest{
			Request: delegation.Request{
				Target: "worker",
				Input:  "do the thing",
				Metadata: map[string]string{
					delegation.ParentRunMetadataKey: "parent-run",
					delegation.CallIDMetadataKey:    "call-1",
				},
			},
			Caller: "caller",
			Depth:  2,
		}},
		Result: &kanban.Result{Response: delegation.Response{
			Output: "done",
			Error:  "",
		}},
	}
	dto, ok := cardDTO(card)
	if !ok {
		t.Fatal("cardDTO rejected a valid card")
	}
	if dto.Target != "worker" || dto.Input != "do the thing" ||
		dto.Output != "done" || dto.Caller != "caller" ||
		dto.Depth != 2 || dto.ParentRunID != "parent-run" ||
		dto.CallID != "call-1" || dto.UpdatedAt == "" {
		t.Fatalf("delegation card = %+v", dto)
	}
}
