package state_test

import (
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// TestResolveTurnTiming pins the display rule for turn timestamps: a
// column the archive never recorded (an empty string, which the store's
// time reader loads as the zero value) reads as the turn's own `at`, and
// the duration comes from the recorded pair only.
func TestResolveTurnTiming(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	requested := at.Add(-2 * time.Minute)
	started := at.Add(-90 * time.Second)
	finished := at

	for _, tc := range []struct {
		name                             string
		at, requested, started, finished time.Time
		wantRequested, wantStarted       time.Time
		wantFinished                     time.Time
		wantDuration                     time.Duration
	}{
		{
			name: "every timestamp recorded",
			at:   at, requested: requested, started: started, finished: finished,
			wantRequested: requested, wantStarted: started, wantFinished: finished,
			wantDuration: 90 * time.Second,
		},
		{
			name: "request anchor missing",
			at:   at, started: started, finished: finished,
			wantRequested: at, wantStarted: started, wantFinished: finished,
			wantDuration: 90 * time.Second,
		},
		{
			name: "turn never started",
			at:   at, requested: requested,
			wantRequested: requested, wantStarted: at, wantFinished: at,
			wantDuration: 0,
		},
		{
			name:          "nothing but the landing time",
			at:            at,
			wantRequested: at, wantStarted: at, wantFinished: at,
			wantDuration: 0,
		},
		{
			name:          "no timestamps at all",
			wantRequested: time.Time{}, wantStarted: time.Time{},
			wantFinished: time.Time{}, wantDuration: 0,
		},
		{
			name: "clock went backwards",
			at:   at, requested: requested, started: at, finished: started,
			wantRequested: requested, wantStarted: at, wantFinished: started,
			wantDuration: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timing := state.ResolveTurnTiming(
				tc.at, tc.requested, tc.started, tc.finished)
			if !timing.At.Equal(tc.at) {
				t.Errorf("at = %v, want %v", timing.At, tc.at)
			}
			if !timing.RequestedAt.Equal(tc.wantRequested) {
				t.Errorf("requested_at = %v, want %v",
					timing.RequestedAt, tc.wantRequested)
			}
			if !timing.StartedAt.Equal(tc.wantStarted) {
				t.Errorf("started_at = %v, want %v",
					timing.StartedAt, tc.wantStarted)
			}
			if !timing.FinishedAt.Equal(tc.wantFinished) {
				t.Errorf("finished_at = %v, want %v",
					timing.FinishedAt, tc.wantFinished)
			}
			if timing.Duration != tc.wantDuration {
				t.Errorf("duration = %v, want %v",
					timing.Duration, tc.wantDuration)
			}
		})
	}
}
