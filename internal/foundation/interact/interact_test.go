package interact

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// TestFromPromptSeverity covers the rule the UI depends on: an explicit
// metadata value wins, an unknown one degrades to the kind's default
// (so a typo can never make a danger prompt look like a question), and
// a prompt without metadata is info for text and notice for the kinds
// that hand the user a decision.
func TestFromPromptSeverity(t *testing.T) {
	cases := []struct {
		name string
		kind string
		meta string
		want Severity
	}{
		{name: "text without metadata", kind: "", meta: "", want: SeverityInfo},
		{name: "select defaults to notice", kind: "select", want: SeverityNotice},
		{name: "confirm defaults to notice", kind: "confirm", want: SeverityNotice},
		{name: "explicit danger wins", kind: "confirm", meta: "danger", want: SeverityDanger},
		{name: "explicit info wins over select default", kind: "select", meta: "info", want: SeverityInfo},
		{name: "unknown value falls back to the kind default", kind: "select", meta: "urgent", want: SeverityNotice},
		{name: "unknown value on text falls back to info", kind: "", meta: "urgent", want: SeverityInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]string{}
			if tc.kind != "" {
				meta[MetaKind] = tc.kind
			}
			if tc.meta != "" {
				meta[MetaSeverity] = tc.meta
			}
			spec := FromPrompt(agent.UserPrompt{
				Parts:    []message.Part{message.TextPart{Text: "q"}},
				Metadata: meta,
			}, "p-1", "r-1", "t-1")
			if spec.Severity != tc.want {
				t.Fatalf("severity = %q, want %q", spec.Severity, tc.want)
			}
		})
	}
}
