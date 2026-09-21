package core

import (
	"encoding/json"
	"testing"
	"time"
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
				now, tc.durationMs,
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
	marshal := func(mutate func(*TurnEndEvent)) map[string]any {
		t.Helper()
		ev := NewTurnEnd(
			"r-1", "s-1", "completed", "", "", "", "done", now, 10,
		)
		mutate(&ev)
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

	// A known zero is omitted: the frontend reads a missing field as
	// "everything was delivered" and that is exactly what zero means.
	got := marshal(func(*TurnEndEvent) {})
	if _, present := got["steer_pending"]; present {
		t.Fatalf("steer_pending present for a delivered turn: %v", got)
	}
	if _, present := got["steer_pending_unknown"]; present {
		t.Fatalf("steer_pending_unknown present for a known count: %v", got)
	}

	// A nonzero count travels as itself, the unknown flag stays out.
	got = marshal(func(ev *TurnEndEvent) { ev.SteerPending = 2 })
	if v, ok := got["steer_pending"].(float64); !ok || v != 2 {
		t.Fatalf("steer_pending = %v, want 2", got["steer_pending"])
	}
	if _, present := got["steer_pending_unknown"]; present {
		t.Fatalf("steer_pending_unknown present for a known count: %v", got)
	}

	// An unreadable count travels as the unknown flag instead of a zero
	// the UI would trust.
	got = marshal(func(ev *TurnEndEvent) { ev.SteerPendingUnknown = true })
	if v, ok := got["steer_pending_unknown"].(bool); !ok || !v {
		t.Fatalf("steer_pending_unknown = %v, want true",
			got["steer_pending_unknown"])
	}
	if _, present := got["steer_pending"]; present {
		t.Fatalf("steer_pending present for an unknown count: %v", got)
	}
}
