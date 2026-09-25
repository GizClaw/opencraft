package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// newSQLiteTurnStore opens a throwaway workspace DB, wraps it in the
// summary.TurnStore adapter used by the deploy assembly, and returns the
// state store so tests can append transcript rows the way the session
// store does.
func newSQLiteTurnStore(t *testing.T) (*sqliteTurnStore, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := compat.WorkspaceSchema(context.Background(), store.Handle()); err != nil {
		t.Fatal(err)
	}
	return &sqliteTurnStore{db: store.Handle()}, store
}

// commitTurn archives one turn through the state store: the archive keeps
// the messages as exchanged (canonical parts, no rendered text), and the
// projection renders them on read.
func commitTurn(
	t *testing.T,
	store *state.Store,
	conv, runID string,
	msgs []message.Message,
) {
	t.Helper()
	if runID == "" {
		runID = "turn-1"
	}
	arch := make([]state.ArchiveMessage, 0, len(msgs))
	for _, m := range msgs {
		arch = append(arch, state.ArchiveMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	if err := store.CommitConversationTurn(
		context.Background(), state.Conversation{ID: conv},
		state.ArchiveTurn{RunID: runID}, arch,
	); err != nil {
		t.Fatalf("commit turn: %v", err)
	}
}

// TestSQLiteTurnStoreProjectsTranscript covers the projection contract:
// reads walk the transcript backward, rows carry their immutable seq, and
// a bounded walk returns exactly the requested rows.
func TestSQLiteTurnStoreProjectsTranscript(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	commitTurn(t, store, conv, "turn-1", []message.Message{
		message.NewTextMessage(message.RoleUser, "m0"),
		message.NewTextMessage(message.RoleAssistant, "m1"),
		message.NewTextMessage(message.RoleUser, "m2"),
	})

	maxSeq, err := adapter.MaxSeq(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if maxSeq != 2 {
		t.Fatalf("MaxSeq = %d, want 2", maxSeq)
	}

	all, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("load all = %d rows, want 3", len(all))
	}
	for i, row := range all {
		if row.Seq != int64(i) {
			t.Errorf("row %d seq = %d, want %d", i, row.Seq, i)
		}
		if want := "m" + string(rune('0'+i)); row.Content.Text() != want {
			t.Errorf("row %d = %q, want %q", i, row.Content.Text(), want)
		}
	}

	// The newest two rows, in chronological order.
	back, err := adapter.LoadBack(ctx, conv, maxSeq+1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].Content.Text() != "m1" ||
		back[1].Content.Text() != "m2" {
		t.Fatalf("LoadBack(3, 2) = %+v, want m1, m2", back)
	}

	// A walk past the start of the conversation returns what exists.
	head, err := adapter.LoadBack(ctx, conv, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 1 || head[0].Content.Text() != "m0" {
		t.Fatalf("LoadBack(1, 5) = %+v, want only m0", head)
	}

	// An empty conversation has no seqs and no rows.
	emptySeq, err := adapter.MaxSeq(ctx, "s-absent")
	if err != nil {
		t.Fatal(err)
	}
	if emptySeq != -1 {
		t.Fatalf("MaxSeq(absent) = %d, want -1", emptySeq)
	}
}

// TestSQLiteTurnStoreRendersToolActivityOnRead pins the read-side
// rendering: the archive stores the tool parts as exchanged, and the
// projection appends the rendered tool activity so the model reads the
// same text the write path used to store alongside them.
func TestSQLiteTurnStoreRendersToolActivityOnRead(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-tool"
	commitTurn(t, store, conv, "turn-1", []message.Message{
		{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "fetch",
					Arguments: json.RawMessage(`{}`),
				}},
			}},
		},
		{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("ok"),
				}},
			}},
		},
	})

	rows, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the call and its result", len(rows))
	}
	calls := rows[0].Message().ToolCalls()
	if rows[0].Role != message.RoleAssistant || len(calls) != 1 || calls[0].ID != "c1" {
		t.Fatalf("call row = %+v, want assistant c1 preserved", rows[0])
	}
	results := rows[1].Message().ToolResults()
	if rows[1].Role != message.RoleTool || len(results) != 1 ||
		results[0].CallID != "c1" {
		t.Fatalf("result row = %+v, want tool c1 preserved", rows[1])
	}
	if text := rows[0].Content.Text(); text == "" {
		t.Fatal("call row has no rendered tool activity")
	}
	if text := rows[1].Content.Text(); text == "" {
		t.Fatal("result row has no rendered tool activity")
	}
}

// TestSQLiteTurnStoreProjectsTextlessRows covers the rows that carry no
// text of their own. An attachment-only row has a prompt form — a
// placeholder naming what it carries — so it enters the window at its own
// coordinate; a payload that does not decode as canonical content, or that
// has nothing a prompt can carry, is left out: counted and warned, never
// silently. Neither renumbers the rows around them: seqs are the
// transcript's, not the window's.
func TestSQLiteTurnStoreProjectsTextlessRows(t *testing.T) {
	capture := logcapture.Install(t)
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	image, err := media.NewImageURL("https://example.invalid/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	commitTurn(t, store, conv, "turn-1", []message.Message{
		message.NewTextMessage(message.RoleUser, "kept"),
		// An attachment-only row: the archive keeps it (the transcript is
		// the conversation) and the window says what it is.
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.ImagePart{Source: image},
		}}},
	})
	// Rows no current writer produces, but a legacy or foreign writer can
	// have left behind: a payload migration 011 never rewrote, and one an
	// interrupted write truncated.
	var turnID int64
	if err := store.Handle().SQLDB().QueryRowContext(ctx,
		`SELECT id FROM archive_turns WHERE conversation_id = ? ORDER BY id LIMIT 1`,
		conv).Scan(&turnID); err != nil {
		t.Fatalf("archive turn id: %v", err)
	}
	for seq, payload := range map[int64]string{2: `{}`, 3: `{"text":"legacy"}`} {
		if _, err := store.Handle().SQLDB().ExecContext(ctx, `
			INSERT INTO archive_messages(
				turn_id, conversation_id, seq, role, content_json, created_at
			) VALUES (?, ?, ?, 'user', ?, '2026-09-01T10:00:00Z')`,
			turnID, conv, seq, payload,
		); err != nil {
			t.Fatalf("seed seq %d: %v", seq, err)
		}
	}
	// A decodable row with nothing to show: a reasoning trace whose text
	// the provider withheld (signature only). Valid content, no prompt
	// form.
	commitTurn(t, store, conv, "turn-2", []message.Message{
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.ReasoningPart{Signature: "opaque"},
		}}},
	})
	commitTurn(t, store, conv, "turn-3", []message.Message{
		message.NewTextMessage(message.RoleAssistant, "kept-2"),
	})

	maxSeq, err := adapter.MaxSeq(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if maxSeq != 5 {
		t.Fatalf("MaxSeq = %d, want 5 (every archived row has a seq)", maxSeq)
	}
	all, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		seq  int64
		text string
	}{
		{0, "kept"},
		{1, "[attachment: image]"},
		{5, "kept-2"},
	}
	if len(all) != len(want) {
		t.Fatalf("rows = %d, want %d (%+v)", len(all), len(want), all)
	}
	for i, w := range want {
		if all[i].Seq != w.seq || all[i].Content.Text() != w.text {
			t.Errorf("row %d = seq %d %q, want seq %d %q",
				i, all[i].Seq, all[i].Content.Text(), w.seq, w.text)
		}
	}

	// The placeholder and the three skipped rows are reported, with counts.
	bodies := capture.Bodies()
	placeholder := capturedRecord(capture, "memory: transcript rows rendered as placeholders")
	if placeholder == nil {
		t.Fatalf("no placeholder record emitted; bodies: %v", bodies)
	}
	if got := logcapture.Attribute(*placeholder, "rows"); got != "1" {
		t.Errorf("placeholder rows = %q, want 1", got)
	}
	skipped := capturedRecord(capture, "memory: transcript rows left out of the window")
	if skipped == nil {
		t.Fatalf("no skip record emitted; bodies: %v", bodies)
	}
	if got := logcapture.Attribute(*skipped, "undecodable"); got != "2" {
		t.Errorf("undecodable rows = %q, want 2 (an empty payload and a legacy one)", got)
	}
	if got := logcapture.Attribute(*skipped, "unrenderable"); got != "1" {
		t.Errorf("unrenderable rows = %q, want 1 (the withheld reasoning trace)", got)
	}
}

// capturedRecord returns the first record captured so far whose body is
// want, or nil when nothing matched.
func capturedRecord(capture *logcapture.Recorder, want string) *sdklog.Record {
	for _, record := range capture.Records() {
		if record.Body().AsString() == want {
			matched := record
			return &matched
		}
	}
	return nil
}

// TestSQLiteTurnStoreSeqAdvancesAcrossCommits verifies seqs continue
// across separate turns, so the transcript stays one dense, append-only
// sequence and a Seq identifies a row for the life of the conversation.
func TestSQLiteTurnStoreSeqAdvancesAcrossCommits(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	commitTurn(t, store, conv, "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "a")})
	commitTurn(t, store, conv, "turn-2", []message.Message{
		message.NewTextMessage(message.RoleAssistant, "b"),
		message.NewTextMessage(message.RoleUser, "c"),
	})

	all, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("rows = %d, want 3", len(all))
	}
	for i, row := range all {
		if row.Seq != int64(i) {
			t.Fatalf("row %d seq = %d, want %d", i, row.Seq, i)
		}
		if want := "abc"[i : i+1]; row.Content.Text() != want {
			t.Errorf("row %d = %q, want %q", i, row.Content.Text(), want)
		}
	}
	back, err := adapter.LoadBack(ctx, conv, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].Content.Text() != "a" ||
		back[1].Content.Text() != "b" {
		t.Fatalf("LoadBack(2, 5) = %+v, want a, b", back)
	}
}

// TestAssemblyPairAwareFoldOnSQLiteTurnStore drives the pair-aware fold
// boundary through the real transcript: the archive keeps the tool pair
// as exchanged, FoldOnly advances a boundary that would split the pair,
// and the extension replays it with real roles.
func TestAssemblyPairAwareFoldOnSQLiteTurnStore(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-pair"
	a := summary.NewAssembly(adapter, summary.WithAssemblyPolicy(summary.Policy{
		MaxRawMessages: 1, PreserveRecent: 1, MaxSummaryBytes: 4096,
	}))
	raw := []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleUser, "context one"),
		message.NewTextMessage(message.RoleUser, "context two"),
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "fetch",
				Arguments: json.RawMessage(`{}`),
			}},
		}}},
		{Role: message.RoleTool, Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: message.NewTextContent("ok"),
			}},
		}}},
		message.NewTextMessage(message.RoleUser, "next"),
	}
	commitTurn(t, store, conv, "t-1", raw)
	if err := a.FoldOnly(ctx, conv); err != nil {
		t.Fatalf("FoldOnly: %v", err)
	}

	loaded, err := adapter.LoadAll(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(raw) {
		t.Fatalf("rows = %d, want %d (every row with a prompt form)", len(loaded), len(raw))
	}
	calls := loaded[3].Message().ToolCalls()
	results := loaded[4].Message().ToolResults()
	if len(calls) != 1 || calls[0].ID != "c1" ||
		len(results) != 1 || results[0].CallID != "c1" {
		t.Fatalf("projected pair = %d calls/%d results, want c1/c1",
			len(calls), len(results))
	}

	res, err := a.Context(ctx, corememory.ContextRequest{
		Scope:          corememory.Scope{RuntimeID: "rt", AgentID: "a"},
		ConversationID: conv,
		Budget:         corememory.Budget{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raws []corememory.ContextItem
	for _, item := range res.Items {
		if item.Kind == corememory.ContextRawMessage {
			raws = append(raws, item)
		}
	}
	if len(raws) != 3 {
		t.Fatalf("raw items = %d, want call + result + tail (%+v)",
			len(raws), res.Items)
	}
	wantRoles := []message.Role{
		message.RoleAssistant, message.RoleTool, message.RoleUser,
	}
	for i, want := range wantRoles {
		if raws[i].MessageRole != want {
			t.Fatalf("raw item %d role = %q, want %q", i, raws[i].MessageRole, want)
		}
	}
	// Raw item ids are transcript coordinates, so the window and the fold
	// coverage speak the same language.
	if raws[0].ID != summary.SourceID(3) {
		t.Fatalf("first raw item id = %q, want seq 3", raws[0].ID)
	}
}

// TestSQLiteTurnStoreSummaryNodes covers the summary node roundtrip:
// upsert (insert + replace), list, delete-by-level-keeping-one, and
// delete-by-id (how another generation's coverage is retired).
func TestSQLiteTurnStoreSummaryNodes(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"
	// A summary node describes an existing conversation's transcript:
	// the writer refuses nodes for a conversation that is not there (a
	// deleted one), so the fixture seeds the row it folds.
	if err := store.EnsureConversation(ctx,
		state.Conversation{ID: conv, Title: "summary"}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	now := time.Now().UTC()
	n1 := summary.SummaryNode{
		ID:        "node-1",
		ThreadID:  conv,
		Level:     0,
		ParentIDs: nil,
		SourceIDs: []string{summary.SourceID(0), summary.SourceID(1)},
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "folded"}}},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			summary.IdentityKey: summary.IdentitySeqV1,
		},
	}
	if err := adapter.UpsertSummaryNode(ctx, n1); err != nil {
		t.Fatal(err)
	}

	// Insert.
	nodes, err := adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].ID != "node-1" || nodes[0].Content.Text() != "folded" {
		t.Errorf("node = %+v", nodes[0])
	}
	if len(nodes[0].SourceIDs) != 2 || nodes[0].SourceIDs[1] != summary.SourceID(1) {
		t.Errorf("source ids = %v", nodes[0].SourceIDs)
	}
	if !nodes[0].CurrentGeneration() {
		t.Errorf("node generation = %q, want %q",
			nodes[0].Generation(), summary.IdentitySeqV1)
	}

	// Replace (upsert same id).
	n1.Content = message.Content{Parts: []message.Part{message.TextPart{Text: "re-folded"}}}
	if err := adapter.UpsertSummaryNode(ctx, n1); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Content.Text() != "re-folded" {
		t.Fatalf("after upsert = %+v, want one re-folded node", nodes)
	}

	// Add a level-1 node and a level-0 sibling, then delete level 0
	// keeping node-1: only the level-0 sibling (node-3) is removed;
	// node-1 is kept and the level-1 node-2 is untouched.
	if err := adapter.UpsertSummaryNode(ctx, summary.SummaryNode{
		ID:        "node-2",
		ThreadID:  conv,
		Level:     1,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "top"}}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UpsertSummaryNode(ctx, summary.SummaryNode{
		ID:        "node-3",
		ThreadID:  conv,
		Level:     0,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "sibling"}}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteSummaryNodes(ctx, conv, 0, "node-1"); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ID != "node-1" || nodes[1].ID != "node-2" {
		t.Fatalf("after delete = %+v, want node-1 kept and node-2 untouched", nodes)
	}

	// An empty keepID removes every node at the level.
	if err := adapter.DeleteSummaryNodes(ctx, conv, 0, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "node-2" {
		t.Fatalf("after delete-all level 0 = %+v, want only node-2", nodes)
	}

	// Delete by id retires exactly the named nodes, and only in the
	// conversation that owns them.
	if err := store.EnsureConversation(ctx,
		state.Conversation{ID: "s-2", Title: "other"}); err != nil {
		t.Fatalf("seed other conversation: %v", err)
	}
	if err := adapter.UpsertSummaryNode(ctx, summary.SummaryNode{
		ID:        "node-4",
		ThreadID:  "s-2",
		Level:     0,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: "other"}}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteSummaryNodesByID(ctx, conv, []string{"node-2"}); err != nil {
		t.Fatal(err)
	}
	nodes, err = adapter.ListSummaryNodes(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("after delete-by-id = %+v, want none", nodes)
	}
	other, err := adapter.ListSummaryNodes(ctx, "s-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].ID != "node-4" {
		t.Fatalf("other conversation nodes = %+v, want node-4 untouched", other)
	}
}

// TestSQLiteTurnStoreIsolation verifies conversations do not share rows:
// every conversation has its own transcript and seq space.
func TestSQLiteTurnStoreIsolation(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()

	commitTurn(t, store, "s-1", "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "conv-a")})
	commitTurn(t, store, "s-2", "turn-1",
		[]message.Message{message.NewTextMessage(message.RoleUser, "conv-b")})

	// Each conversation keeps its own rows and its own seq space.
	want := map[string]string{"s-1": "conv-a", "s-2": "conv-b"}
	for conv, text := range want {
		all, err := adapter.LoadAll(ctx, conv)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 || all[0].Content.Text() != text || all[0].Seq != 0 {
			t.Errorf("conv %s: %+v, want one row %q at seq 0", conv, all, text)
		}
	}
}
