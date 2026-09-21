package automations

import (
	"testing"
	"time"
)

// TestTaskTimeoutDuration pins the effective bound: an explicit
// duration wins, everything else falls back to the default.
func TestTaskTimeoutDuration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout string
		want    time.Duration
	}{
		{name: "unset", timeout: "", want: DefaultTaskTimeout},
		{name: "explicit", timeout: "2h", want: 2 * time.Hour},
		{name: "short", timeout: "90m", want: 90 * time.Minute},
		{name: "unparsable falls back", timeout: "15", want: DefaultTaskTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := Task{Timeout: tc.timeout}
			if got := task.TimeoutDuration(); got != tc.want {
				t.Fatalf("TimeoutDuration(%q) = %s, want %s",
					tc.timeout, got, tc.want)
			}
		})
	}
}

// TestTaskValidateTimeout pins the accepted shape: empty means the
// default, a positive duration up to a day is allowed, and anything
// else fails on save instead of silently running unbounded.
func TestTaskValidateTimeout(t *testing.T) {
	base := func(timeout string) Task {
		return Task{
			Name:      "brief",
			Prompt:    "run",
			Workspace: "/tmp/ws",
			Mode:      ModeWorkspace,
			Schedule:  Schedule{Type: ScheduleDaily, Time: "09:00"},
			Timeout:   timeout,
		}
	}
	for _, tc := range []struct {
		name    string
		timeout string
		wantErr bool
	}{
		{name: "unset", timeout: ""},
		{name: "quarter hour", timeout: "15m"},
		{name: "a day", timeout: "24h"},
		{name: "past a day", timeout: "25h", wantErr: true},
		{name: "unitless", timeout: "15", wantErr: true},
		{name: "negative", timeout: "-5m", wantErr: true},
		{name: "zero", timeout: "0s", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := base(tc.timeout).Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(%q) accepted", tc.timeout)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(%q) = %v", tc.timeout, err)
			}
		})
	}
}
