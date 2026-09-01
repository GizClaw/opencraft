package automations

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

func TestScheduleDaily(t *testing.T) {
	loc := time.Local
	after := time.Date(2026, 9, 1, 8, 0, 0, 0, loc) // 09:00 not yet reached
	next, err := (Schedule{Type: ScheduleDaily, Time: "09:00"}).Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 1, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("daily next = %v, want %v", next, want)
	}

	after = time.Date(2026, 9, 1, 9, 0, 1, 0, loc)
	next, err = (Schedule{Type: ScheduleDaily, Time: "09:00"}).Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 2, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("daily next = %v, want %v", next, want)
	}
}

func TestScheduleWeekdays(t *testing.T) {
	loc := time.Local
	sched := Schedule{Type: ScheduleWeekdays, Time: "09:00"}

	// Friday evening rolls to Monday.
	after := time.Date(2026, 9, 4, 18, 0, 0, 0, loc) // Friday
	next, err := sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekdays next = %v, want %v", next, want)
	}

	// Weekend stays on Monday.
	after = time.Date(2026, 9, 5, 10, 0, 0, 0, loc) // Saturday
	next, err = sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekdays weekend next = %v, want %v", next, want)
	}
}

func TestScheduleWeekly(t *testing.T) {
	loc := time.Local
	sched := Schedule{Type: ScheduleWeekly, Days: []string{"MO", "TH"}, Time: "10:30"}

	after := time.Date(2026, 9, 3, 10, 31, 0, 0, loc) // Thursday
	next, err := sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 10, 30, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekly next = %v, want %v", next, want)
	}

	after = time.Date(2026, 9, 7, 10, 29, 0, 0, loc) // Monday just before
	next, err = sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 10, 30, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("weekly same-day next = %v, want %v", next, want)
	}
}

func TestScheduleHourly(t *testing.T) {
	loc := time.Local
	sched := Schedule{Type: ScheduleHourly, IntervalHours: 2}
	after := time.Date(2026, 9, 1, 17, 0, 0, 0, loc)
	next, err := sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 1, 19, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("hourly next = %v, want %v", next, want)
	}

	// Day filtering steps over the weekend and keeps the phase.
	sched = Schedule{
		Type: ScheduleHourly, IntervalHours: 2,
		Days: []string{"MO", "TU", "WE", "TH", "FR"},
	}
	after = time.Date(2026, 9, 4, 23, 0, 0, 0, loc) // Friday 23:00
	next, err = sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 1, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("hourly days next = %v, want %v", next, want)
	}
}

func TestScheduleCron(t *testing.T) {
	loc := time.Local
	sched := Schedule{Type: ScheduleCron, Cron: "0 9 * * 1-5"}
	after := time.Date(2026, 9, 4, 9, 1, 0, 0, loc) // Friday 09:01
	next, err := sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("cron next = %v, want %v", next, want)
	}

	sched = Schedule{Type: ScheduleCron, Cron: "*/15 9-17 * * *"}
	after = time.Date(2026, 9, 1, 9, 0, 0, 0, loc)
	next, err = sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 1, 9, 15, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("cron step next = %v, want %v", next, want)
	}

	sched = Schedule{Type: ScheduleCron, Cron: "30 8 1 JAN MON"}
	after = time.Date(2026, 9, 1, 0, 0, 0, 0, loc)
	if _, err := sched.Next(after); err != nil {
		t.Fatalf("cron names should parse: %v", err)
	}
}

func TestScheduleCronRejectsUnsupported(t *testing.T) {
	for _, expr := range []string{
		"* * * *",     // four fields
		"0 9 L * *",   // L
		"0 9 * * 5#2", // #
		"0 9 ? * *",   // ?
		"0 9 * 13 *",  // out of range month
		"0 9 * * 8",   // out of range dow
		"0 25 * * *",  // out of range hour
		"0 9 5-1 * *", // backwards range
		"*/0 9 * * *", // zero step
		"0 9 * * * *", // six fields (seconds)
	} {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q) should fail", expr)
		}
	}
}

func TestScheduleDSTWallClock(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	// 2026-03-08 02:00-03:00 does not exist (spring forward). Go's
	// time.Date does not guarantee which side of the transition a
	// missing wall time lands on, so only assert the schedule advances
	// without erroring on the transition day.
	sched := Schedule{Type: ScheduleDaily, Time: "02:30"}
	after := time.Date(2026, 3, 8, 1, 0, 0, 0, loc)
	next, err := sched.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	if !next.After(after) || !next.Before(after.Add(48*time.Hour)) {
		t.Fatalf("DST daily next = %v, want within 48h after %v", next, after)
	}
}
