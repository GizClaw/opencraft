package host

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

// TestPendingSteerReadsResultState pins the reading rules of the
// undelivered-steer count: an absent key is a known zero (core writes
// it only when something is pending), a value this build cannot
// understand is unknown, and no result at all is unknown too — the
// caller must keep every steered row rather than trust a zero.
// TestPendingSteerReadsTheRealTurnResult pins the other half, that the
// key core actually writes is the one read here.
func TestPendingSteerReadsResultState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		res       *agent.Result
		want      int
		wantKnown bool
	}{
		{
			name: "no result",
			res:  nil, want: 0, wantKnown: false,
		},
		{name: "no state", res: &agent.Result{}, want: 0, wantKnown: true},
		{
			name: "missing key",
			res:  &agent.Result{State: map[string]any{"other": 1}},
			want: 0, wantKnown: true,
		},
		{
			name: "recorded count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": 2,
			}},
			want: 2, wantKnown: true,
		},
		{
			// A result state that crossed JSON carries numbers as
			// float64; whole values are the same count.
			name: "json-decoded count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": float64(3),
			}},
			want: 3, wantKnown: true,
		},
		{
			name: "unexpected type",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": "2",
			}},
			want: 0, wantKnown: false,
		},
		{
			name: "negative count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": -1,
			}},
			want: 0, wantKnown: false,
		},
		{
			name: "fractional count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": 1.5,
			}},
			want: 0, wantKnown: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := PendingSteer(tc.res)
			if got != tc.want || known != tc.wantKnown {
				t.Fatalf("PendingSteer = %d/%v, want %d/%v",
					got, known, tc.want, tc.wantKnown)
			}
		})
	}
}
