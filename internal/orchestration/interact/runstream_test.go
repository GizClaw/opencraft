package interact

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/event"
)

func TestStreamRunID(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{"agent.run.run-123.stream.assistant.delta", "run-123"},
		{"engine.run.run-456.stream.assistant.delta", "run-456"},
		// A three-segment subject still carries a run id after the
		// agent|engine.run prefix; shorter or mis-shaped subjects do
		// not.
		{"agent.run.run-789", "run-789"},
		{"agent.run", ""},
		{"agent.other.run-789.stream", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := StreamRunID(event.Subject(tt.subject)); got != tt.want {
			t.Errorf("StreamRunID(%q) = %q, want %q",
				tt.subject, got, tt.want)
		}
	}
}
