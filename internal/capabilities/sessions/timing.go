package sessions

import "github.com/GizClaw/opencraft/internal/capabilities/sessions/state"

// Timing resolves the timestamps one archived turn displays with. A
// timestamp the archive never recorded is stored as an empty string —
// state.parseTime reads that as the zero value, and forks and exports
// write those columns back verbatim — and a reader must not invent a
// fallback of its own. The rule (a never-recorded timestamp reads as the
// turn's own at, and the duration comes from the recorded pair) lives in
// state.ResolveTurnTiming; this method is how a display consumer reaches
// it.
func (t TurnRecord) Timing() state.TurnTiming {
	return state.ResolveTurnTiming(t.At, t.RequestedAt, t.StartedAt, t.FinishedAt)
}
