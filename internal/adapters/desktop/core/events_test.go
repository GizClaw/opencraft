package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
)

func TestTurnEndEventCarriesDurationMs(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		durationMs int64
	}{
		{name: "nonzero", durationMs: 123_000},
		{name: "zero", durationMs: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := NewTurnEnd(
				"r-1", "s-1", "completed", "", "req-1", "resp-1", "done",
				now, tc.durationMs, &agent.Result{},
			)
			raw, err := json.Marshal(ev)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			durationMs, ok := got["duration_ms"].(float64)
			if !ok || durationMs != float64(tc.durationMs) {
				t.Fatalf("duration_ms = %v, want %d (%s)",
					got["duration_ms"], tc.durationMs, raw)
			}
			if got["finished_at"] != now.Format(time.RFC3339) {
				t.Fatalf("finished_at = %v, want %q",
					got["finished_at"], now.Format(time.RFC3339))
			}
			if got["request_id"] != "req-1" {
				t.Fatalf("request_id = %v, want req-1", got["request_id"])
			}
			if got["response_id"] != "resp-1" {
				t.Fatalf("response_id = %v, want resp-1", got["response_id"])
			}
		})
	}
}

func TestTurnEndEventSteerPendingWireShape(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	marshal := func(res *agent.Result) map[string]any {
		t.Helper()
		ev := NewTurnEnd(
			"r-1", "s-1", "completed", "", "", "", "done", now, 10, res,
		)
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// A delivered turn sends a literal zero, never an omitted field: the
	// frontend treats an absent (or null) value as an unknown count, so
	// "nothing pending" has to travel as itself.
	got := marshal(&agent.Result{})
	if v, ok := got["steer_pending"].(float64); !ok || v != 0 {
		t.Fatalf("steer_pending = %v, want 0", got["steer_pending"])
	}

	// A recorded count travels as itself.
	got = marshal(&agent.Result{State: map[string]any{
		"session.pending_steer": 2,
	}})
	if v, ok := got["steer_pending"].(float64); !ok || v != 2 {
		t.Fatalf("steer_pending = %v, want 2", got["steer_pending"])
	}

	// A count this build cannot read — like no result at all — travels
	// as null instead of a zero the UI would trust.
	got = marshal(&agent.Result{State: map[string]any{
		"session.pending_steer": "2",
	}})
	if v, present := got["steer_pending"]; !present || v != nil {
		t.Fatalf("steer_pending = %v (present %v), want null", v, present)
	}
	got = marshal(nil)
	if v, present := got["steer_pending"]; !present || v != nil {
		t.Fatalf("steer_pending = %v (present %v), want null", v, present)
	}
}

func TestSteerPendingEventWireShape(t *testing.T) {
	raw, err := json.Marshal(SteerPendingEvent{
		RunID:          "r-1",
		ConversationID: "s-1",
		SteerPending:   0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	// The live count is read by the same reconciliation as the one
	// turn_end carries, so it travels under the same key and a drained
	// queue sends a literal zero rather than omitting the field.
	v, ok := got["steer_pending"].(float64)
	if !ok || v != 0 {
		t.Fatalf("steer_pending = %v (%s), want 0", got["steer_pending"], raw)
	}
	if got["run_id"] != "r-1" || got["conversation_id"] != "s-1" {
		t.Fatalf("ids = %v/%v, want r-1/s-1", got["run_id"], got["conversation_id"])
	}
}
