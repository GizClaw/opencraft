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

// ScheduleType is one of the four supported scheduling modes.
type ScheduleType string

const (
	ScheduleHourly   ScheduleType = "hourly"
	ScheduleDaily    ScheduleType = "daily"
	ScheduleWeekdays ScheduleType = "weekdays"
	ScheduleWeekly   ScheduleType = "weekly"
)

// Schedule is the time rule of one task.
type Schedule struct {
	Type          ScheduleType `json:"type"`
	IntervalHours int          `json:"interval_hours,omitempty"`
	// IntervalWeeks is the weekly cadence (1 = every week). It is only
	// meaningful for ScheduleWeekly.
	IntervalWeeks int      `json:"interval_weeks,omitempty"`
	Days          []string `json:"days,omitempty"`
	Time          string   `json:"time,omitempty"`
	// Origin anchors the weekly phase: on-weeks are counted from the
	// Monday of Origin (RFC3339 date, e.g. "2026-09-07"). It is owned
	// by the backend; empty falls back to the anchor's own week.
	Origin string `json:"origin,omitempty"`
}

// Validate checks the schedule shape.
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
			if s.IntervalWeeks < 0 {
				return fmt.Errorf("weekly: interval_weeks must be >= 1")
			}
			if s.Origin != "" {
				if _, err := time.Parse("2006-01-02", s.Origin); err != nil {
					return fmt.Errorf("weekly: origin must be YYYY-MM-DD")
				}
			}
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
	r, err := s.recurrenceFromSchedule()
	if err != nil {
		return time.Time{}, err
	}
	return r.next(after)
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
		if s.IntervalWeeks > 1 {
			return fmt.Sprintf("weekly %dw %s %s", s.IntervalWeeks,
				strings.Join(s.Days, ","), s.Time)
		}
		return "weekly " + strings.Join(s.Days, ",") + " " + s.Time
	}
	return string(s.Type)
}

// ensureOrigin sets the weekly phase anchor to today when the schedule
// is weekly and no origin is persisted yet.
func (s *Schedule) ensureOrigin(now time.Time) {
	if s.Type == ScheduleWeekly && s.Origin == "" {
		s.Origin = now.Format("2006-01-02")
	}
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
	ThinkMinimal = "minimal"
	ThinkLow     = "low"
	ThinkMedium  = "medium"
	ThinkHigh    = "high"
	ThinkXHigh   = "xhigh"
)

// Task is one repeatable automation configuration.
type Task struct {
	ID        string
	Name      string
	Prompt    string
	Schedule  Schedule
	Workspace string
	Mode      string
	Model     string
	Think     string
	// ConversationID optionally pins every run to one existing
	// session: runs reuse that conversation (with its own mode/think/
	// model) instead of minting a fresh one. Empty = new session per
	// run.
	ConversationID string
	Notify         string
	Enabled        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastRunAt      time.Time
	LastStatus     string
	NextRunAt      time.Time
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
	case "", ThinkMinimal, ThinkLow, ThinkMedium, ThinkHigh, ThinkXHigh:
	default:
		return fmt.Errorf("unknown think level %q", t.Think)
	}
	if t.ConversationID != "" && !strings.HasPrefix(t.ConversationID, "s-") {
		return fmt.Errorf("conversation must be a session id")
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
