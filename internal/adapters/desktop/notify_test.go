package desktop

import (
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
)

func TestNotifyStatusUsesTexts(t *testing.T) {
	zh := core.TextsFor("zh")
	if got := notifyStatus(zh, "completed", ""); got != "任务完成" {
		t.Fatalf("completed status = %q, want zh copy", got)
	}
	en := core.TextsFor("en")
	if got := notifyStatus(en, "canceled", ""); got != "Task cancelled" {
		t.Fatalf("canceled status = %q, want en copy", got)
	}
	// The engine reports a deadline as a canceled status; the error
	// kind is what keeps the banner from blaming the user.
	if got := notifyStatus(en, "canceled", "timeout"); got != "Task timed out" {
		t.Fatalf("timeout status = %q, want timeout copy", got)
	}
	// An automation run has the status of its own; the banner names it
	// instead of leaking the enum.
	if got := notifyStatus(en, "timeout", ""); got != "Task timed out" {
		t.Fatalf("automation timeout status = %q, want timeout copy", got)
	}
	if got := notifyStatus(en, "some-other", ""); got != "some-other" {
		t.Fatalf("unknown status = %q, want passthrough", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	got := truncateRunes("一二三四五六", 4)
	if got != "一二三四…" {
		t.Fatalf("truncateRunes = %q, want 4 runes + ellipsis", got)
	}
	if short := truncateRunes("abc", 5); short != "abc" {
		t.Fatalf("short string altered: %q", short)
	}
}

// automationNotification is the shaping half of the automation banner;
// the payload reaches it through Shell.Notify because the frontend has
// no consumer for it.
func TestAutomationNotificationShapesBanner(t *testing.T) {
	en := core.TextsFor("en")
	for _, tc := range []struct {
		name      string
		payload   map[string]any
		wantTitle string
		wantBody  string
	}{
		{
			name: "task name becomes the title and output the snippet",
			payload: map[string]any{
				"name": "nightly brief", "status": "completed",
				"output": "  three findings  ",
			},
			wantTitle: "nightly brief",
			wantBody:  "Task finished\nthree findings",
		},
		{
			name: "error is the snippet when the task produced no output",
			payload: map[string]any{
				"name": "nightly brief", "status": "failed",
				"error": "provider timeout",
			},
			wantTitle: "nightly brief",
			wantBody:  "Task failed\nprovider timeout",
		},
		{
			name:      "a nameless task falls back to the app name",
			payload:   map[string]any{"status": "completed"},
			wantTitle: notifyFallbackTitle,
			wantBody:  "Task finished",
		},
	} {
		title, body := automationNotification(en, tc.payload)
		if title != tc.wantTitle || body != tc.wantBody {
			t.Errorf("%s: banner = %q / %q, want %q / %q",
				tc.name, title, body, tc.wantTitle, tc.wantBody)
		}
	}
}

func TestAutomationNotificationTruncatesLongSnippet(t *testing.T) {
	long := strings.Repeat("x", notifySnippetLimit+50)
	_, body := automationNotification(
		core.TextsFor("en"),
		map[string]any{"name": "brief", "status": "completed", "output": long},
	)
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("snippet not truncated: %q", body)
	}
	if len([]rune(body)) > notifySnippetLimit+len("Task finished")+2 {
		t.Fatalf("body longer than the banner allows: %d runes", len([]rune(body)))
	}
}
