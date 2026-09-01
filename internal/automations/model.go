// Package automations implements the user-level scheduled task
// subsystem: task/schedule/run models, next-run computation, SQLite
// persistence in ~/.opencraft/user.db, and the scheduling manager.
// It has no desktop dependency so it can be unit-tested standalone.
package automations

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ScheduleType is one of the five supported scheduling modes.
type ScheduleType string

const (
	ScheduleHourly   ScheduleType = "hourly"
	ScheduleDaily    ScheduleType = "daily"
	ScheduleWeekdays ScheduleType = "weekdays"
	ScheduleWeekly   ScheduleType = "weekly"
	ScheduleCron     ScheduleType = "cron"
)

// Schedule is the time rule of one task.
type Schedule struct {
	Type          ScheduleType `json:"type"`
	IntervalHours int          `json:"interval_hours,omitempty"`
	Days          []string     `json:"days,omitempty"`
	Time          string       `json:"time,omitempty"`
	Cron          string       `json:"cron,omitempty"`
}

// Validate checks the schedule shape. It does not parse cron itself;
// Next does that and returns an error when the expression is invalid.
func (s Schedule) Validate() error {
	switch s.Type {
	case ScheduleHourly:
		if s.IntervalHours < 1 {
			return fmt.Errorf("hourly: interval_hours must be >= 1")
		}
		if err := validateDays(s.Days, false); err != nil {
			return err
		}
	case ScheduleDaily, ScheduleWeekdays, ScheduleWeekly:
		if !validClock(s.Time) {
			return fmt.Errorf("%s: time must be HH:MM", s.Type)
		}
		if s.Type == ScheduleWeekly {
			if err := validateDays(s.Days, true); err != nil {
				return err
			}
		}
	case ScheduleCron:
		if strings.TrimSpace(s.Cron) == "" {
			return fmt.Errorf("cron: expression is required")
		}
		if _, err := parseCron(s.Cron); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown schedule type %q", s.Type)
	}
	return nil
}

// Next returns the next trigger strictly after after, per §4.2 of the
// design doc. The schedule must be valid (Validate or Next will
// reject bad input).
func (s Schedule) Next(after time.Time) (time.Time, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, err
	}
	switch s.Type {
	case ScheduleHourly:
		return s.nextHourly(after)
	case ScheduleDaily:
		return nextClock(after, s.Time, 2, nil)
	case ScheduleWeekdays:
		return nextClock(after, s.Time, 7, weekdayOK)
	case ScheduleWeekly:
		set := daysSet(s.Days)
		return nextClock(after, s.Time, 8, func(d time.Weekday) bool {
			return set[d]
		})
	case ScheduleCron:
		spec, err := parseCron(s.Cron)
		if err != nil {
			return time.Time{}, err
		}
		return spec.next(after)
	}
	return time.Time{}, fmt.Errorf("unknown schedule type %q", s.Type)
}

// Description returns a short human-readable schedule summary for the
// task list (en-US text; the UI owns its own formatting).
func (s Schedule) Description() string {
	switch s.Type {
	case ScheduleHourly:
		if len(s.Days) > 0 {
			return fmt.Sprintf("every %dh (%s)", s.IntervalHours, strings.Join(s.Days, ","))
		}
		return fmt.Sprintf("every %dh", s.IntervalHours)
	case ScheduleDaily:
		return "daily " + s.Time
	case ScheduleWeekdays:
		return "weekdays " + s.Time
	case ScheduleWeekly:
		return "weekly " + strings.Join(s.Days, ",") + " " + s.Time
	case ScheduleCron:
		return "cron " + s.Cron
	}
	return string(s.Type)
}

func (s Schedule) nextHourly(after time.Time) (time.Time, error) {
	days := daysSet(s.Days)
	interval := time.Duration(s.IntervalHours) * time.Hour
	for t := after.Add(interval); ; t = t.Add(interval) {
		if days == nil || days[t.Weekday()] {
			return t, nil
		}
		if t.After(after.AddDate(0, 0, 366)) {
			return time.Time{}, fmt.Errorf("hourly: no next run within a year")
		}
	}
}

// nextClock computes the next wall-clock occurrence of time "HH:MM"
// after after, scanning at most maxDays for a date accepted by ok
// (nil accepts every day).
func nextClock(after time.Time, clock string, maxDays int, ok func(time.Weekday) bool) (time.Time, error) {
	h, m, err := parseClock(clock)
	if err != nil {
		return time.Time{}, err
	}
	for day := 0; day < maxDays; day++ {
		t := time.Date(
			after.Year(), after.Month(), after.Day(), h, m, 0, 0,
			after.Location(),
		).AddDate(0, 0, day)
		if !t.After(after) {
			continue
		}
		if ok == nil || ok(t.Weekday()) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no next run within %d days", maxDays)
}

func validClock(s string) bool {
	_, _, err := parseClock(s)
	return err == nil
}

func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

func weekdayOK(d time.Weekday) bool {
	return d >= time.Monday && d <= time.Friday
}

// daysSet maps weekday abbreviations to Go weekdays. An empty list
// returns nil (every day allowed). Duplicates are allowed by Set.
func daysSet(days []string) map[time.Weekday]bool {
	if len(days) == 0 {
		return nil
	}
	set := make(map[time.Weekday]bool, len(days))
	for _, d := range days {
		if wd, ok := parseWeekday(d); ok {
			set[wd] = true
		}
	}
	return set
}

func validateDays(days []string, required bool) error {
	if len(days) == 0 {
		if required {
			return fmt.Errorf("weekly: days must not be empty")
		}
		return nil
	}
	seen := make(map[time.Weekday]bool, len(days))
	for _, d := range days {
		wd, ok := parseWeekday(d)
		if !ok {
			return fmt.Errorf("invalid weekday %q", d)
		}
		seen[wd] = true
	}
	return nil
}

func parseWeekday(s string) (time.Weekday, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "MO", "MON", "MONDAY":
		return time.Monday, true
	case "TU", "TUE", "TUESDAY":
		return time.Tuesday, true
	case "WE", "WED", "WEDNESDAY":
		return time.Wednesday, true
	case "TH", "THU", "THURSDAY":
		return time.Thursday, true
	case "FR", "FRI", "FRIDAY":
		return time.Friday, true
	case "SA", "SAT", "SATURDAY":
		return time.Saturday, true
	case "SU", "SUN", "SUNDAY":
		return time.Sunday, true
	}
	return 0, false
}

// Mode values mirror the per-session sandbox modes.
const (
	ModeWorkspace = "workspace"
	ModeReadOnly  = "read-only"
	ModeYOLO      = "yolo"
)

// Think levels mirror the per-session reasoning effort values.
const (
	ThinkLow    = "low"
	ThinkMedium = "medium"
	ThinkHigh   = "high"
)

// Task is one repeatable automation configuration.
type Task struct {
	ID         string
	Name       string
	Prompt     string
	Schedule   Schedule
	Workspace  string
	Mode       string
	Model      string
	Think      string
	Notify     string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastRunAt  time.Time
	LastStatus string
	NextRunAt  time.Time
}

// Validate checks task-level fields. Workspace existence is checked by
// the desktop layer (the path may be valid but not currently open).
func (t Task) Validate() error {
	name := strings.TrimSpace(t.Name)
	if name == "" || len([]rune(name)) > 200 {
		return fmt.Errorf("name must be 1-200 characters")
	}
	prompt := strings.TrimSpace(t.Prompt)
	if prompt == "" || len([]rune(prompt)) > 10000 {
		return fmt.Errorf("prompt must be 1-10000 characters")
	}
	if !filepath.IsAbs(t.Workspace) {
		return fmt.Errorf("workspace must be an absolute path")
	}
	switch t.Mode {
	case "", ModeWorkspace, ModeReadOnly, ModeYOLO:
	default:
		return fmt.Errorf("unknown mode %q", t.Mode)
	}
	switch t.Think {
	case "", ThinkLow, ThinkMedium, ThinkHigh:
	default:
		return fmt.Errorf("unknown think level %q", t.Think)
	}
	switch t.Notify {
	case "", NotifyAlways, NotifyFailed, NotifyNever:
	default:
		return fmt.Errorf("unknown notify policy %q", t.Notify)
	}
	return t.Schedule.Validate()
}

// Notification policies for one task.
const (
	NotifyAlways = "always"
	NotifyFailed = "failed"
	NotifyNever  = "never"
)

// RunStatus is the terminal/live status of one run.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunSkipped   RunStatus = "skipped"
)

// Run is one execution record of a task.
type Run struct {
	ID             string
	TaskID         string
	At             time.Time
	Status         RunStatus
	Error          string
	ConversationID string
	RunID          string
	DurationMs     int64
	Summary        string
}

// RunResult is what the desktop runner returns for one completed run.
type RunResult struct {
	Status         RunStatus
	Error          string
	ConversationID string
	RunID          string
}

// NewID returns a fresh prefixed random id (t-<hex> for tasks,
// run_<hex> for runs).
func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}
