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
				"r-1", "s-1", "completed", "", "done",
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
		})
	}
}
