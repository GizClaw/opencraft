package automations

import (
	"fmt"
	"time"
)

// frequency is the base cadence of one recurrence rule.
type frequency int

const (
	freqHourly frequency = iota
	freqDaily
	freqWeekly
)

// timeOfDay is a wall-clock HH:MM in the schedule's local location.
type timeOfDay struct {
	hour, minute int
}

// recurrence is the generic next-occurrence engine behind the four
// user-facing schedule modes:
//
//   - hourly: candidates every interval hours, optionally restricted
//     to the selected weekdays (nil weekdays = every day), phase kept
//     from the anchor;
//   - daily: every interval days at at, optionally restricted to the
//     selected weekdays (weekdays mode = daily interval 1 + Mon–Fri);
//   - weekly: on-weeks are counted from origin's Monday every interval
//     weeks, and the next selected weekday at at inside an on-week is
//     returned. Without an origin (legacy schedules) the first
//     selected weekday in the anchor's own week is used.
//
// Wall-clock modes build candidates with time.Date + AddDate so DST
// transitions follow the local clock; hourly uses absolute durations.
type recurrence struct {
	freq     frequency
	interval int // hours (hourly) or days (daily); weekly scans 7 days
	at       timeOfDay
	weekdays map[time.Weekday]bool // nil = every day
	origin   time.Time             // weekly phase anchor (zero = unanchored)
}

// maxHourlyLookahead bounds hourly scans so impossible weekday filters
// fail instead of looping forever.
const maxHourlyLookahead = 366 * 24 * time.Hour

// maxDailyLookahead bounds daily scans to a calendar year.
const maxDailyLookahead = 366 * 7

// maxWeeklyLookahead bounds phase-anchored weekly scans.
const maxWeeklyLookahead = 5 * 366

// next returns the next occurrence strictly after after.
func (r recurrence) next(after time.Time) (time.Time, error) {
	switch r.freq {
	case freqHourly:
		return r.nextHourly(after)
	case freqDaily:
		return r.nextDaily(after)
	case freqWeekly:
		return r.nextWeekly(after)
	}
	return time.Time{}, fmt.Errorf("recurrence: unknown frequency %d", r.freq)
}

func (r recurrence) dayOK(d time.Weekday) bool {
	return r.weekdays == nil || r.weekdays[d]
}

func (r recurrence) nextHourly(after time.Time) (time.Time, error) {
	step := time.Duration(r.interval) * time.Hour
	limit := after.Add(maxHourlyLookahead)
	for t := after.Add(step); t.Before(limit); t = t.Add(step) {
		if r.dayOK(t.Weekday()) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("hourly: no next run within a year")
}

// dayAt builds the wall-clock candidate day days after after in the
// anchor's location.
func (r recurrence) dayAt(after time.Time, day int) time.Time {
	return time.Date(
		after.Year(), after.Month(), after.Day(),
		r.at.hour, r.at.minute, 0, 0,
		after.Location(),
	).AddDate(0, 0, day)
}

func (r recurrence) nextDaily(after time.Time) (time.Time, error) {
	// Day 0 is "today at HH:MM": it counts when it is still ahead of
	// the anchor, so interval 1 keeps the daily "today or tomorrow"
	// behaviour while larger intervals skip whole days.
	for day := 0; day <= maxDailyLookahead; day += r.interval {
		t := r.dayAt(after, day)
		if !t.After(after) {
			continue
		}
		if r.dayOK(t.Weekday()) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("daily: no next run within a year")
}

func (r recurrence) nextWeekly(after time.Time) (time.Time, error) {
	if !r.origin.IsZero() {
		return r.nextWeeklyAnchored(after)
	}
	// The immediate next occurrence is the first selected weekday in
	// the anchor's week (day 7 covers the same weekday next week when
	// the anchor is past today's time). This is the legacy fallback
	// for schedules saved before the phase anchor existed.
	for day := 0; day <= 7; day++ {
		t := r.dayAt(after, day)
		if !t.After(after) {
			continue
		}
		if r.dayOK(t.Weekday()) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("weekly: no selected weekday within 7 days")
}

// nextWeeklyAnchored returns the next occurrence whose week is aligned
// with origin (every interval weeks, counted from origin's Monday).
func (r recurrence) nextWeeklyAnchored(after time.Time) (time.Time, error) {
	interval := r.interval
	if interval < 1 {
		interval = 1
	}
	// The origin is a date; interpret it in the anchor's location so
	// wall-clock candidates follow the same local time zone.
	originLocal := time.Date(
		r.origin.Year(), r.origin.Month(), r.origin.Day(),
		0, 0, 0, 0, after.Location(),
	)
	originStart := weekStart(originLocal)
	limit := after.AddDate(0, 0, maxWeeklyLookahead)
	for start := originStart; start.Before(limit); start = start.AddDate(0, 0, interval*7) {
		for day := 0; day < 7; day++ {
			t := time.Date(
				start.Year(), start.Month(), start.Day(),
				r.at.hour, r.at.minute, 0, 0,
				start.Location(),
			).AddDate(0, 0, day)
			if !t.After(after) {
				continue
			}
			if r.dayOK(t.Weekday()) {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("weekly: no next run within five years")
}

// weekStart returns the Monday 00:00 of t's week in t's location.
func weekStart(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday = 0
	return time.Date(
		t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location(),
	).AddDate(0, 0, -offset)
}

// recurrenceFromSchedule maps one validated schedule to its engine
// rule.
func (s Schedule) recurrenceFromSchedule() (recurrence, error) {
	switch s.Type {
	case ScheduleHourly:
		return recurrence{
			freq:     freqHourly,
			interval: s.IntervalHours,
			weekdays: daysSet(s.Days),
		}, nil
	case ScheduleDaily:
		h, m, err := parseClock(s.Time)
		if err != nil {
			return recurrence{}, err
		}
		return recurrence{
			freq: freqDaily, interval: 1,
			at: timeOfDay{hour: h, minute: m},
		}, nil
	case ScheduleWeekdays:
		h, m, err := parseClock(s.Time)
		if err != nil {
			return recurrence{}, err
		}
		return recurrence{
			freq: freqDaily, interval: 1,
			at:       timeOfDay{hour: h, minute: m},
			weekdays: weekdaySet(),
		}, nil
	case ScheduleWeekly:
		h, m, err := parseClock(s.Time)
		if err != nil {
			return recurrence{}, err
		}
		r := recurrence{
			freq: freqWeekly, interval: 1,
			at:       timeOfDay{hour: h, minute: m},
			weekdays: daysSet(s.Days),
		}
		if s.IntervalWeeks >= 1 {
			r.interval = s.IntervalWeeks
		}
		if s.Origin != "" {
			origin, err := time.Parse("2006-01-02", s.Origin)
			if err != nil {
				return recurrence{}, fmt.Errorf("weekly: origin must be YYYY-MM-DD")
			}
			r.origin = origin
		}
		return r, nil
	}
	return recurrence{}, fmt.Errorf("unknown schedule type %q", s.Type)
}

// weekdaySet is the Monday–Friday filter used by the weekdays mode.
func weekdaySet() map[time.Weekday]bool {
	set := make(map[time.Weekday]bool, 5)
	for d := time.Monday; d <= time.Friday; d++ {
		set[d] = true
	}
	return set
}
