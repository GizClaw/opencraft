package state

import "time"

// TurnTiming is the timing of one archived turn as a reader displays
// it: the four stored timestamps with the gaps filled in, plus the
// worked-for duration when the archive recorded one.
type TurnTiming struct {
	At          time.Time
	RequestedAt time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	// Duration is finish − start when the archive recorded both and
	// they are ordered, zero otherwise. It is measured from the stored
	// pair, not from the fallbacks above: a turn that never finished
	// has no duration to show even though FinishedAt displays as `at`.
	Duration time.Duration
}

// ResolveTurnTiming applies the one rule for the turn timestamps a
// reader displays. The archive stores an empty string for a timestamp it
// never recorded (parseTime reads that as the zero value, and forks and
// exports write those columns back verbatim), and such a timestamp reads
// as the turn's own `at` — the moment the turn landed. Callers must not
// invent a fallback of their own: this is the single place the display
// gap (and, one line down, the duration derived from it) is resolved.
func ResolveTurnTiming(at, requestedAt, startedAt, finishedAt time.Time) TurnTiming {
	timing := TurnTiming{
		At:          at,
		RequestedAt: timeOr(requestedAt, at),
		StartedAt:   timeOr(startedAt, at),
		FinishedAt:  timeOr(finishedAt, at),
	}
	if !startedAt.IsZero() && !finishedAt.IsZero() &&
		finishedAt.After(startedAt) {
		timing.Duration = finishedAt.Sub(startedAt)
	}
	return timing
}

// timeOr returns t, or fallback when t was never recorded.
func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
