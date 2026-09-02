package automations

import (
	"testing"
	"time"
)

func TestRecurrenceDailyIntervalSkipsDays(t *testing.T) {
	loc := time.Local
	r := recurrence{
		freq: freqDaily, interval: 2,
		at: timeOfDay{hour: 9, minute: 0},
	}
	// 08:00 today is before 09:00: day 0 counts.
	next, err := r.next(time.Date(2026, 9, 1, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 1, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("daily interval next = %v, want %v", next, want)
	}
	// Past 09:00: the next candidate is two days later.
	next, err = r.next(time.Date(2026, 9, 1, 9, 0, 1, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 3, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("daily interval next = %v, want %v", next, want)
	}
}

func TestRecurrenceDailyWeekdayFilter(t *testing.T) {
	loc := time.Local
	r := recurrence{
		freq: freqDaily, interval: 1,
		at:       timeOfDay{hour: 9, minute: 0},
		weekdays: weekdaySet(),
	}
	// Saturday 08:00 → Monday 09:00.
	next, err := r.next(time.Date(2026, 9, 5, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekday filter next = %v, want %v", next, want)
	}
}

func TestRecurrenceWeeklyNextWeekday(t *testing.T) {
	loc := time.Local
	origin, err := time.ParseInLocation("2006-01-02", "2026-08-31", loc)
	if err != nil {
		t.Fatal(err)
	}
	r := recurrence{
		freq: freqWeekly, interval: 1,
		at:       timeOfDay{hour: 9, minute: 0},
		weekdays: daysSet([]string{"MO"}),
		origin:   origin,
	}
	// Wednesday → the current window's Monday.
	next, err := r.next(time.Date(2026, 9, 2, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekly next = %v, want %v", next, want)
	}
	// Past that Monday: the same weekday next week (day 7).
	next, err = r.next(time.Date(2026, 9, 7, 9, 0, 1, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 14, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekly next = %v, want %v", next, want)
	}
}

func TestRecurrenceWeeklyAnchoredInterval(t *testing.T) {
	loc := time.Local
	origin, err := time.ParseInLocation("2006-01-02", "2026-09-07", loc)
	if err != nil {
		t.Fatal(err)
	}
	r := recurrence{
		freq: freqWeekly, interval: 2,
		at:       timeOfDay{hour: 9, minute: 0},
		weekdays: daysSet([]string{"MO"}),
		origin:   origin,
	}
	// Wednesday of the previous week → the first on-week's Monday.
	next, err := r.next(time.Date(2026, 9, 2, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("anchored weekly next = %v, want %v", next, want)
	}
	// Past that Monday → the same weekday two weeks later.
	next, err = r.next(time.Date(2026, 9, 7, 9, 0, 1, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 21, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("anchored weekly next = %v, want %v", next, want)
	}
	// Tuesday inside the on-week → the next on-week's Monday.
	next, err = r.next(time.Date(2026, 9, 8, 8, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 21, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("anchored weekly next = %v, want %v", next, want)
	}
}

func TestScheduleWeeklyIntervalWeeks(t *testing.T) {
	loc := time.Local
	sched := Schedule{
		Type:          ScheduleWeekly,
		Days:          []string{"MO"},
		Time:          "09:00",
		IntervalWeeks: 2,
		Origin:        "2026-09-07",
	}
	next, err := sched.Next(time.Date(2026, 9, 7, 9, 0, 1, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 21, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekly interval next = %v, want %v", next, want)
	}
}

func TestRecurrenceHourlyKeepsPhase(t *testing.T) {
	loc := time.Local
	r := recurrence{freq: freqHourly, interval: 2}
	next, err := r.next(time.Date(2026, 9, 1, 17, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 1, 19, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("hourly phase next = %v, want %v", next, want)
	}
}
