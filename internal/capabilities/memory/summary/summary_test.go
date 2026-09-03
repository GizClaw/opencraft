package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

func textMessages(threadID string, texts ...string) []message.Message {
	out := make([]message.Message, 0, len(texts))
	for _, text := range texts {
		out = append(out, message.NewTextMessage(message.RoleUser, text))
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
	pol := Policy{MaxRawMessages: 2, PreserveRecent: 2}
	msgs := textMessages("t1", "m01", "m02", "m03", "m04", "m05", "m06")
	// foldBoundary = 6 - 2 - 2 = 2 -> m01, m02 are fold candidates.
	prev, err := BufferFold(pol, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil {
		t.Fatal("want a fold for 6 messages with raw 2 + preserve 2")
	}
	if len(prev.SourceIDs) != 2 {
		t.Fatalf("source ids = %v, want 2 folded messages", prev.SourceIDs)
	}
	summaryText := prev.Content.Text()
	if !strings.Contains(summaryText, "m01") || !strings.Contains(summaryText, "m02") {
		t.Fatalf("summary missing folded text: %q", summaryText)
	}

	// Same input, same prev -> nothing new to fold (idempotent).
	again, err := BufferFold(pol, "t1", msgs, prev, now)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("want nil when nothing new, got %+v", again)
	}
}

func TestBufferFoldRuneSafeTruncation(t *testing.T) {
	now := time.Now()
	big := strings.Repeat("中", 5000)
	msgs := textMessages("t1", big, "mid", "recent")
	// foldBoundary = 3 - 1 - 1 = 1 -> only `big` is a fold candidate; it
	// alone overflows MaxSummaryBytes=100 and must be kept, truncated.
	got, err := BufferFold(Policy{MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 100}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("want fold for 3 messages with raw 1 + preserve 1")
	}
	gotText := got.Content.Text()
	if len(gotText) > 100 {
		t.Fatalf("summary bytes %d > 100", len(gotText))
	}
	if !strings.HasSuffix(gotText, "中") {
		t.Fatalf("summary must not split a rune: %q", gotText)
	}
	// SourceIDs must cover the one rendered message, no more.
	if len(got.SourceIDs) != 1 {
		t.Fatalf("source ids = %v, want exactly the truncated message", got.SourceIDs)
	}
}

func TestBufferFoldRollingWindowKeepsNewestWhenFull(t *testing.T) {
	// The freeze bug: once the summary is full, chaining prev + new folds
	// and tail-truncating discarded every new fold. The rolling window must
	// keep the NEWEST foldable messages and drop the OLDEST instead.
	now := time.Now()
	msgs := textMessages("t1", "old-01", "old-02", "old-03", "new-01", "new-02", "new-03", "new-04")
	// foldBoundary = 7 - 2 - 2 = 3 -> foldable = old-01..old-03. The budget
	// fits exactly one message ("user: old-03" = 12 bytes).
	node, err := BufferFold(Policy{MaxRawMessages: 2, PreserveRecent: 2, MaxSummaryBytes: 12}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("want a fold")
	}
	text := node.Content.Text()
	if !strings.Contains(text, "old-03") {
		t.Fatalf("newest foldable message must survive, got %q", text)
	}
	if strings.Contains(text, "old-01") {
		t.Fatalf("oldest foldable message must be dropped when full, got %q", text)
	}
	// Honest coverage: SourceIDs exactly match the rendered text.
	if len(node.SourceIDs) != 1 {
		t.Fatalf("source ids = %v, want only the rendered message", node.SourceIDs)
	}
}

func TestBufferFoldAdvancesWhenSummaryFull(t *testing.T) {
	// A saturated summary must keep advancing with the conversation instead
	// of freezing on the first content that filled the budget.
	now := time.Now()
	pol := Policy{MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 10}

	first, err := BufferFold(pol, "t1", textMessages("t1", "a", "b", "c", "d"), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || !strings.Contains(first.Content.Text(), "b") {
		t.Fatalf("first fold = %+v, want newest foldable 'b'", first)
	}

	second, err := BufferFold(pol, "t1", textMessages("t1", "a", "b", "c", "d", "e"), first, now)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatal("want a new fold when the conversation advanced")
	}
	if !strings.Contains(second.Content.Text(), "c") {
		t.Fatalf("summary must advance, got %q", second.Content.Text())
	}
	if second.ID != first.ID {
		t.Fatalf("node id changed across folds (%s -> %s): level-0 node must be stable",
			first.ID, second.ID)
	}
}

func TestBufferFoldNodeIDStableAcrossFolds(t *testing.T) {
	now := time.Now()
	pol := Policy{MaxRawMessages: 2, PreserveRecent: 2, MaxSummaryBytes: 4096}

	first, err := BufferFold(pol, "t1", textMessages("t1", "m1", "m2", "m3", "m4", "m5", "m6", "m7"), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BufferFold(pol, "t1", textMessages("t1", "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"), first, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil {
		t.Fatalf("want folds, got %v / %v", first, second)
	}
	if first.ID != second.ID {
		t.Fatalf("level-0 node id must be stable so the store replaces in place, got %s vs %s",
			first.ID, second.ID)
	}
	if !strings.Contains(second.Content.Text(), "m4") {
		t.Fatalf("second fold must include the newly foldable message, got %q", second.Content.Text())
	}
}

func TestBufferFoldSourceIDsMatchRenderedText(t *testing.T) {
	// A message dropped by the rolling window must not appear in SourceIDs.
	now := time.Now()
	pol := Policy{MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 10}
	msgs := textMessages("t1", "aaaaaaaaaaaaaaaaaaaaaaaaaa", "bb", "cc", "dd")
	// foldBoundary = 4 - 1 - 1 = 2 -> foldable = [aa, bb]. `bb` fits, `aa`
	// (32 bytes) does not, so `aa` is dropped.
	node, err := BufferFold(pol, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("want a fold")
	}
	if len(node.SourceIDs) != 1 {
		t.Fatalf("source ids = %v, want only the kept message", node.SourceIDs)
	}
	if !strings.Contains(node.Content.Text(), "bb") {
		t.Fatalf("kept message missing from summary: %q", node.Content.Text())
	}
	if strings.Contains(node.Content.Text(), "aa") {
		t.Fatalf("dropped message must not appear in summary: %q", node.Content.Text())
	}
}

func TestBufferFoldPreserveRecentParticipates(t *testing.T) {
	now := time.Now()
	msgs := textMessages("t1", "a", "b", "c", "d")

	// MaxRaw=1, PreserveRecent=1: foldBoundary = 4-1-1 = 2 -> folds.
	folded, err := BufferFold(Policy{MaxRawMessages: 1, PreserveRecent: 1}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if folded == nil {
		t.Fatal("want a fold with PreserveRecent=1")
	}

	// MaxRaw=1, PreserveRecent=3: Normalize lifts MaxRaw to 3, and
	// foldBoundary = 4-3-3 < 0 -> nothing folds.
	notFolded, err := BufferFold(Policy{MaxRawMessages: 1, PreserveRecent: 3}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if notFolded != nil {
		t.Fatal("want no fold with PreserveRecent=3")
	}
}

func TestBufferFoldLineSafeTruncation(t *testing.T) {
	now := time.Now()
	big := "line1\nline2\nline3\n" + strings.Repeat("x", 5000)
	msgs := textMessages("t1", big, "mid", "recent")
	// foldBoundary = 3 - 1 - 1 = 1 -> only `big` is a fold candidate and it
	// alone overflows, so it is truncated at a line boundary.
	node, err := BufferFold(Policy{MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 100}, "t1", msgs, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("want a fold")
	}
	text := node.Content.Text()
	if len(text) > 100 {
		t.Fatalf("summary bytes %d > 100", len(text))
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("truncation must stop at a line boundary, got %q", text)
	}
	if strings.Contains(text, "x") {
		t.Fatalf("truncation must not split a line, got %q", text)
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
