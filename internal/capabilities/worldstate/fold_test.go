package worldstate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// The durable fold's write half: a channel range becomes transcript rows
// through the pairing this service rendered, and a range that does not line up
// with it is refused rather than shadowing the wrong rows on a later read.

func foldItem(seq int64, role message.Role, parts ...message.Part) corememory.ContextItem {
	return corememory.ContextItem{
		Kind:        corememory.ContextRawMessage,
		MessageRole: role,
		Content:     message.Content{Parts: parts},
		Sequence:    uint64(seq),
	}
}

func TestNormalizeRawItemsWithSeqsPairsEveryRenderedRow(t *testing.T) {
	call := message.ToolCallPart{Call: message.ToolCall{
		ID: "call-1", Name: "read", Arguments: []byte(`{}`),
	}}
	orphan := message.ToolResultPart{Result: message.ToolResult{
		CallID: "call-other", Content: message.NewTextContent("orphan"),
	}}
	items := []corememory.ContextItem{
		foldItem(10, message.RoleUser, message.TextPart{Text: "ask"}),
		// A call whose result never arrives this batch: the renderer inserts
		// a synthetic aborted result right after it, which inherits its row.
		foldItem(11, message.RoleAssistant, call),
		// An orphan result is dropped, and must not shift the pairing.
		foldItem(12, message.RoleTool, orphan),
		foldItem(13, message.RoleAssistant, message.TextPart{Text: "done"}),
	}
	msgs, seqs := normalizeRawItemsWithSeqs(items)
	if len(msgs) != len(seqs) {
		t.Fatalf("rendered %d messages but %d seqs", len(msgs), len(seqs))
	}
	want := []int64{10, 11, 11, 13}
	if len(msgs) != len(want) {
		t.Fatalf("rendered %d messages, want %d: %+v", len(msgs), len(want), msgs)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("seq[%d] = %d, want %d (roles %v)", i, seqs[i], want[i], rolesOf(msgs))
		}
	}
}

func rolesOf(msgs []message.Message) []message.Role {
	out := make([]message.Role, len(msgs))
	for i, msg := range msgs {
		out[i] = msg.Role
	}
	return out
}

func foldStore(t *testing.T) (*sessions.Store, string) {
	t.Helper()
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	id, err := store.Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return store, id
}

func TestRecordFoldWritesTheRowRange(t *testing.T) {
	store, conv := foldStore(t)
	board := agent.NewBoard()
	// Two world sections, then the replayed history rows 100..102.
	seqs := []int64{100, 101, 102}
	stashReplaySeqs(board, store, seqs)

	const sections = 2
	if err := recordFold(context.Background(), board, conv, sections, 2, 3, "summary of row 100"); err != nil {
		t.Fatalf("record fold: %v", err)
	}
	var doc foldDocument
	if err := store.ReadState(conv, sessions.DocumentCompactFold, &doc); err != nil {
		t.Fatalf("read fold document: %v", err)
	}
	if doc.Version != 1 || doc.CoveredFrom != 100 || doc.CoveredTo != 100 || doc.Rows != 1 {
		t.Fatalf("document = %+v, want rows 100..100", doc)
	}
	if doc.AtSeq != 102 {
		t.Fatalf("at_seq = %d, want the replay's newest row 102", doc.AtSeq)
	}
	if doc.Summary != "summary of row 100" {
		t.Fatalf("summary = %q", doc.Summary)
	}
}

func TestRecordFoldRefusesARangeOutsideTheReplay(t *testing.T) {
	store, conv := foldStore(t)
	board := agent.NewBoard()
	stashReplaySeqs(board, store, []int64{100, 101})

	for _, tc := range []struct {
		name                 string
		sections, start, end int
	}{
		{name: "before the history", sections: 2, start: 1, end: 3},
		{name: "past the replay", sections: 2, start: 2, end: 9},
		{name: "empty range", sections: 2, start: 2, end: 2},
	} {
		if err := recordFold(context.Background(), board, conv,
			tc.sections, tc.start, tc.end, "summary"); err == nil {
			t.Fatalf("%s: recordFold accepted the range", tc.name)
		}
	}
	// Nothing was written, so a later read cannot shadow anything.
	var doc foldDocument
	if err := store.ReadState(conv, sessions.DocumentCompactFold, &doc); err == nil {
		t.Fatalf("a refused fold wrote a document: %+v", doc)
	}
}

// The durable fold's read half: a replay applies the recorded fold, so the
// model reads the summary plus everything the fold did not touch — the same
// view on every turn, and the reason the transcript stops being folded again.

func writeFold(t *testing.T, store *sessions.Store, conv string, doc foldDocument) {
	t.Helper()
	if err := store.WriteState(conv, sessions.DocumentCompactFold, doc); err != nil {
		t.Fatalf("write fold document: %v", err)
	}
}

func TestReplayAppliesTheDurableFold(t *testing.T) {
	ctx := context.Background()
	store, conv := foldStore(t)
	writeFold(t, store, conv, foldDocument{
		Version: 1, AtSeq: 102, CoveredFrom: 100, CoveredTo: 102,
		Rows: 3, Summary: "rows 100 to 102",
	})

	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)
	svc.memory = replayMemory{stubMemory{items: []corememory.ContextItem{
		foldItem(100, message.RoleUser, message.TextPart{Text: "one"}),
		foldItem(101, message.RoleAssistant, message.TextPart{Text: "two"}),
		foldItem(102, message.RoleUser, message.TextPart{Text: "three"}),
		foldItem(103, message.RoleAssistant, message.TextPart{Text: "kept"}),
	}}}

	msgs, seqs := svc.replayHistory(ctx, conv)
	if len(msgs) != 2 {
		t.Fatalf("replayed %d messages (%v), want the summary and one kept row",
			len(msgs), rolesOf(msgs))
	}
	head := msgs[0].Content.Text()
	if !summarytext.IsSummaryText(head) {
		t.Fatalf("first message is not a fold summary: %q", head)
	}
	if !strings.Contains(head, "rows 100 to 102") {
		t.Fatalf("summary lost its text: %q", head)
	}
	if got := msgs[1].Content.Text(); got != "kept" {
		t.Fatalf("the uncovered row after the span was not kept: %q", got)
	}
	// The summary carries the covered span's first row, so a later fold that
	// covers the summary records the same start.
	if seqs[0] != 100 || seqs[1] != 103 {
		t.Fatalf("seqs = %v, want [100 103]", seqs)
	}
}

func TestReplayWithoutAFoldDocumentKeepsEveryRow(t *testing.T) {
	ctx := context.Background()
	store, conv := foldStore(t)
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)
	svc.memory = replayMemory{stubMemory{items: []corememory.ContextItem{
		foldItem(1, message.RoleUser, message.TextPart{Text: "one"}),
		foldItem(2, message.RoleAssistant, message.TextPart{Text: "two"}),
	}}}
	msgs, _ := svc.replayHistory(ctx, conv)
	if len(msgs) != 2 {
		t.Fatalf("replayed %d messages, want both rows untouched", len(msgs))
	}
}

func TestReplayFoldIgnoresAMalformedDocument(t *testing.T) {
	ctx := context.Background()
	store, conv := foldStore(t)
	// No summary: the fold cannot stand in for the rows it names, so the
	// replay must keep them rather than hide them behind an empty message.
	writeFold(t, store, conv, foldDocument{
		Version: 1, CoveredFrom: 1, CoveredTo: 2, Rows: 2,
	})
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)
	svc.memory = replayMemory{stubMemory{items: []corememory.ContextItem{
		foldItem(1, message.RoleUser, message.TextPart{Text: "one"}),
		foldItem(2, message.RoleAssistant, message.TextPart{Text: "two"}),
	}}}
	msgs, _ := svc.replayHistory(ctx, conv)
	if len(msgs) != 2 {
		t.Fatalf("a malformed fold hid rows: %d messages", len(msgs))
	}
}
