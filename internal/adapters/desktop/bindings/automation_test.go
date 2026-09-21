package bindings

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
)

// TestAutomationTaskDTORoundTripsTimeout pins the run-limit field on the
// UI wire form. The editor holds a duration string that the task stores
// verbatim, so a DTO that dropped the field or reshaped it would reset
// every task to the 15-minute default on the next save — a bound nobody
// chose, applied silently while the user thinks they are editing
// something else.
func TestAutomationTaskDTORoundTripsTimeout(t *testing.T) {
	task := automations.Task{
		ID:        "t-1",
		Name:      "brief",
		Prompt:    "summarize",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: "/tmp/w",
		Mode:      automations.ModeWorkspace,
		Notify:    automations.NotifyAlways,
		Timeout:   "2h",
		Enabled:   true,
	}
	dto := ToAutomationTaskDTO(task)
	if dto.Timeout != "2h" {
		t.Fatalf("dto timeout = %q, want 2h", dto.Timeout)
	}
	// The key is always on the wire, and an empty value means "unset"
	// (the shipped default) rather than a missing field the UI would
	// have to guess about.
	if got := dtoWire(t, dto)["timeout"]; got != "2h" {
		t.Fatalf("wire timeout = %v, want 2h", got)
	}
	if back := FromAutomationTaskDTO(dto); back.Timeout != "2h" {
		t.Fatalf("round-trip timeout = %q, want 2h", back.Timeout)
	}

	dto = ToAutomationTaskDTO(automations.Task{Timeout: ""})
	if got, ok := dtoWire(t, dto)["timeout"]; !ok || got != "" {
		t.Fatalf("wire timeout for an unset limit = %v (present %v)", got, ok)
	}
	if back := FromAutomationTaskDTO(dto); back.Timeout != "" {
		t.Fatalf("round-trip unset timeout = %q, want empty", back.Timeout)
	}

	// A padded duration is trimmed rather than stored verbatim: the
	// stored string is what the editor re-reads on the next open.
	if got := FromAutomationTaskDTO(
		AutomationTaskDTO{Timeout: "  45m  "},
	).Timeout; got != "45m" {
		t.Fatalf("padded timeout = %q, want 45m", got)
	}
}

func dtoWire(t *testing.T, dto AutomationTaskDTO) map[string]any {
	t.Helper()
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal dto: %v", err)
	}
	return wire
}
