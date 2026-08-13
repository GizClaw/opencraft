package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

func textMessages(threadID string, texts ...string) []message.Message {
	out := make([]message.Message, 0, len(texts))
	for i, text := range texts {
		msg := message.NewTextMessage(message.RoleUser, text)
		_ = msg
		out = append(out, msg)
		_ = i
	}
	return out
}

func TestBufferFoldBelowWindow(t *testing.T) {
	got, err := BufferFold(Policy{}, "t1", textMessages("t1", "a", "b"), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("want nil below window, got %+v", got)
	}
}

func TestBufferFoldFoldsAndDedups(t *testing.T) {
	now := time.Now()
	msgs := textMessages("t1", "m01", "m02", "m03", "m04", "m05", "m06")
	prev, err := BufferFold(Policy{MaxRawMessages: 4}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil {
		t.Fatal("want a fold for 6 messages with window 4")
	}
	if len(prev.SourceIDs) != 2 {
		t.Fatalf("source ids = %v, want 2 folded messages", prev.SourceIDs)
	}
	summaryText := prev.Content.Text()
	if !strings.Contains(summaryText, "m01") || !strings.Contains(summaryText, "m02") {
		t.Fatalf("summary missing folded text: %q", summaryText)
	}

	again, err := BufferFold(Policy{MaxRawMessages: 4}, "t1", msgs, prev, now)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("want nil when nothing new, got %+v", again)
	}
}

func TestBufferFoldRuneSafeTruncation(t *testing.T) {
	now := time.Now()
	msgs := textMessages("t1", strings.Repeat("中", 5000), "recent")
	got, err := BufferFold(Policy{MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 100}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("want fold for 2 messages with window 1")
	}
	gotText := got.Content.Text()
	if len(gotText) > 100 {
		t.Fatalf("summary bytes %d > 100", len(gotText))
	}
	if !strings.HasSuffix(gotText, "中") {
		t.Fatalf("summary must not split a rune: %q", gotText)
	}
}

func TestStableMessageIDDeterministic(t *testing.T) {
	a := stableMessageID("t1", 0, message.NewTextMessage(message.RoleUser, "hello"))
	b := stableMessageID("t1", 0, message.NewTextMessage(message.RoleUser, "hello"))
	if a != b {
		t.Fatalf("stable id differs: %s vs %s", a, b)
	}
	c := stableMessageID("t2", 0, message.NewTextMessage(message.RoleUser, "hello"))
	if a == c {
		t.Fatalf("stable id must differ across threads")
	}
}
