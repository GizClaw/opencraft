package desktop

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
)

func TestNotifyStatusUsesTexts(t *testing.T) {
	zh := core.TextsFor("zh")
	if got := notifyStatus(zh, "completed"); got != "任务完成" {
		t.Fatalf("completed status = %q, want zh copy", got)
	}
	en := core.TextsFor("en")
	if got := notifyStatus(en, "canceled"); got != "Task cancelled" {
		t.Fatalf("canceled status = %q, want en copy", got)
	}
	if got := notifyStatus(en, "some-other"); got != "some-other" {
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
