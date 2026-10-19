package desktop

import (
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
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
	// A status this build cannot name is not copy: the banner says the
	// generic thing rather than printing the enum.
	if got := notifyStatus(en, "some-other", ""); got != "Task failed" {
		t.Fatalf("unknown status = %q, want the generic failure copy", got)
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

// A banner id is the replacement key the notification center uses, so it
// has to be the same for the same conversation and kind and different
// for everything else.
func TestBannerIDReplacesPerConversation(t *testing.T) {
	if got := bannerID(bannerTurnEnd, "conv-1"); got != "turn-end:conv-1" {
		t.Fatalf("bannerID = %q, want kind:conversation", got)
	}
	if got := bannerID(bannerTurnEnd, ""); got != bannerTurnEnd {
		t.Fatalf("bannerID without a conversation = %q, want %q",
			got, bannerTurnEnd)
	}
	// Two kinds for one conversation must not collide: the second result
	// of a turn would otherwise replace the banner still asking for
	// input.
	if bannerID(bannerInteract, "conv-1") == bannerID(bannerTurnEnd, "conv-1") {
		t.Fatal("kinds share one banner id")
	}
}

// notificationOpenTarget decides what activating a banner means. Only the
// default action opens a conversation, and only a banner that named one
// gives it somewhere to go.
func TestNotificationOpenTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		data   map[string]any
		want   core.OpenConversationEvent
		ok     bool
	}{
		{
			name:   "default action on a conversation",
			action: notifications.DefaultActionIdentifier,
			data: map[string]any{
				"conversation_id": "conv-1",
				"workspace":       "/w/one",
			},
			want: core.OpenConversationEvent{
				ConversationID: "conv-1",
				Workspace:      "/w/one",
			},
			ok: true,
		},
		{
			name:   "a banner that lost its workspace still opens",
			action: notifications.DefaultActionIdentifier,
			data:   map[string]any{"conversation_id": "conv-1"},
			want:   core.OpenConversationEvent{ConversationID: "conv-1"},
			ok:     true,
		},
		{
			name:   "another action is not an open",
			action: "reply",
			data:   map[string]any{"conversation_id": "conv-1"},
		},
		{
			name:   "a banner without a conversation has nowhere to go",
			action: notifications.DefaultActionIdentifier,
			data:   map[string]any{"workspace": "/w/one"},
		},
		{
			name:   "an empty conversation id is not a target",
			action: notifications.DefaultActionIdentifier,
			data:   map[string]any{"conversation_id": ""},
		},
		{
			name:   "a non-string id is not trusted",
			action: notifications.DefaultActionIdentifier,
			data:   map[string]any{"conversation_id": 7},
		},
		{
			name:   "a response without data has nowhere to go",
			action: notifications.DefaultActionIdentifier,
		},
	} {
		got, ok := notificationOpenTarget(tc.action, tc.data)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: openTarget = %+v / %v, want %+v / %v",
				tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
