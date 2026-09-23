package state_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

func userText(text string) state.ArchiveMessage {
	return state.ArchiveMessage{
		Role: string(message.RoleUser),
		Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: text}},
		},
	}
}

// commitTurn archives one turn with the given messages.
func commitTurn(
	t *testing.T, s *state.Store, conv, title, runID string,
	at time.Time, msgs ...state.ArchiveMessage,
) {
	t.Helper()
	for i := range msgs {
		if msgs[i].CreatedAt.IsZero() {
			msgs[i].CreatedAt = at
		}
	}
	if err := s.CommitConversationTurn(context.Background(), state.Conversation{
		ID: conv, Title: title, CreatedAt: at, UpdatedAt: at,
	}, state.ArchiveTurn{
		RunID:       runID,
		At:          at,
		RequestedAt: at,
		StartedAt:   at,
		FinishedAt:  at,
		Status:      "completed",
	}, msgs); err != nil {
		t.Fatalf("commit turn %s: %v", runID, err)
	}
}

func TestSearchMessagesFindsArchivedMessages(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	commitTurn(t, s, "s-1", "检索方案", "run-1", base,
		userText("我们决定采用 SQLite FTS5 来做跨会话检索"),
		state.ArchiveMessage{
			Role: string(message.RoleAssistant),
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "好的，session_search 会走 trigram 子串匹配。"},
			}},
		},
		state.ArchiveMessage{
			Role: string(message.RoleTool),
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID:  "call-1",
					Content: message.NewTextContent("zebra42quokka appears in the deploy log"),
				}},
			}},
		},
	)
	commitTurn(t, s, "s-2", "deploy notes", "run-2", base.Add(24*time.Hour),
		userText("The deploy pipeline uses flowcraft deploy/resource/graph"),
	)

	// Chinese substring recall: the reason the index tokenizes with
	// trigram instead of unicode61.
	res, err := s.SearchMessages(ctx, "跨会话", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Substring {
		t.Fatalf("expected the trigram index to answer, got a scan fallback")
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %+v, want the one matching message", res.Hits)
	}
	hit := res.Hits[0]
	if hit.ConversationID != "s-1" || hit.Role != string(message.RoleUser) ||
		hit.RunID != "run-1" || hit.TurnSeq != 1 || hit.Seq != 0 {
		t.Fatalf("hit = %+v", hit)
	}
	if !strings.Contains(hit.Snippet, "[跨会话]") {
		t.Fatalf("snippet = %q, want the match marked", hit.Snippet)
	}
	if hit.Title == "" || hit.At.IsZero() {
		t.Fatalf("hit = %+v, want a title and a time", hit)
	}

	// English word, and a token that only exists inside a tool result:
	// both are part of the prompt projection the index stores.
	for _, query := range []string{"pipeline", "zebra42quokka"} {
		res, err := s.SearchMessages(ctx, query, state.SearchOptions{})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if len(res.Hits) != 1 {
			t.Fatalf("search %q hits = %+v, want one", query, res.Hits)
		}
	}
}

func TestSearchMessagesFiltersAndCollapse(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	commitTurn(t, s, "s-1", "", "run-1", base, userText("deploy notes from s-1"))
	commitTurn(t, s, "s-1", "", "run-2", base.Add(time.Hour), userText("more deploy notes in s-1"))
	commitTurn(t, s, "s-2", "", "run-3", base.Add(48*time.Hour), userText("deploy notes in s-2"))

	// One hit per conversation when collapsing, best-ranked first.
	res, err := s.SearchMessages(ctx, "deploy", state.SearchOptions{Collapse: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("collapsed hits = %+v, want one per conversation", res.Hits)
	}
	if res.Hits[0].ConversationID == res.Hits[1].ConversationID {
		t.Fatalf("collapsed hits repeat a conversation: %+v", res.Hits)
	}
	// Collapsing drops the second message of s-1; the message count is
	// not what the result reports, so nothing is truncated.
	if res.Truncated {
		t.Fatalf("collapsed hits = %+v, want no truncation at this limit", res.Hits)
	}

	// Collapsed and limited by conversations.
	res, err = s.SearchMessages(ctx, "deploy", state.SearchOptions{Limit: 1, Collapse: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || !res.Truncated {
		t.Fatalf("collapsed+limited hits = %+v truncated=%v", res.Hits, res.Truncated)
	}

	// Without collapsing every matching message is a hit, bounded by Limit.
	res, err = s.SearchMessages(ctx, "deploy", state.SearchOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || !res.Truncated {
		t.Fatalf("limited hits = %+v truncated=%v, want one and truncated", res.Hits, res.Truncated)
	}

	// Conversation filter and Since.
	res, err = s.SearchMessages(ctx, "deploy", state.SearchOptions{ConversationID: "s-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ConversationID != "s-2" {
		t.Fatalf("conversation filter hits = %+v", res.Hits)
	}
	res, err = s.SearchMessages(ctx, "deploy", state.SearchOptions{Since: base.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ConversationID != "s-2" {
		t.Fatalf("since filter hits = %+v", res.Hits)
	}
}

func TestSearchMessagesShortQueryFallsBackToScan(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	commitTurn(t, s, "s-1", "", "run-1", base, userText("AI 助手与 go 语言 practice"))
	commitTurn(t, s, "s-2", "", "run-2", base, userText("go 语言 only"))

	// Every term shorter than the trigram window: a substring scan, and
	// every term still has to match.
	res, err := s.SearchMessages(ctx, "AI", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Substring {
		t.Fatalf("a two-character query must report the scan fallback")
	}
	if len(res.Hits) != 1 || res.Hits[0].ConversationID != "s-1" {
		t.Fatalf("hits = %+v, want only s-1", res.Hits)
	}
	if !strings.Contains(res.Hits[0].Snippet, "AI") {
		t.Fatalf("snippet = %q, want an excerpt of the message", res.Hits[0].Snippet)
	}

	// A mixed query uses the index for the long term and keeps the
	// short one as a literal filter.
	res, err = s.SearchMessages(ctx, "go practice", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Substring {
		t.Fatalf("a query with a three-character term should use the index")
	}
	if len(res.Hits) != 1 || res.Hits[0].ConversationID != "s-1" {
		t.Fatalf("mixed query hits = %+v, want only s-1", res.Hits)
	}
}

func TestSearchMessagesRejectsEmptyQuery(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	if _, err := s.SearchMessages(context.Background(), "   ", state.SearchOptions{}); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

func TestDeleteConversationRowsDropsSearchRows(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	commitTurn(t, s, "s-1", "", "run-1", base, userText("removable deploy note"))
	commitTurn(t, s, "s-2", "", "run-2", base, userText("kept deploy note"))

	if err := s.DeleteConversationRows(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}
	res, err := s.SearchMessages(ctx, "deploy", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ConversationID != "s-2" {
		t.Fatalf("hits after delete = %+v, want only s-2", res.Hits)
	}
	var indexed int
	if err := s.Handle().SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_fts`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("index rows = %d, want 1", indexed)
	}
}

func TestBackfillSearchIndexesUnindexedMessages(t *testing.T) {
	root := t.TempDir()
	s := openState(t, filepath.Join(root, "session.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	// Rows a pre-016 build wrote: archive rows without index rows.
	content, err := json.Marshal(message.Content{Parts: []message.Part{
		message.TextPart{Text: "旧会话里的跨会话检索决定"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	conn := s.Handle().SQLDB()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO conversations(id, title, created_at, updated_at)
		VALUES ('s-old', 'legacy', ?, ?)`,
		base.Format(time.RFC3339Nano), base.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	res, err := conn.ExecContext(ctx, `
		INSERT INTO archive_turns(
			conversation_id, seq, run_id, at, requested_at, started_at,
			finished_at, status, error, interrupt_cause, error_kind,
			request_id, response_id, artifacts_json)
		VALUES ('s-old', 1, 'run-old', ?, '', '', '', 'completed', '', '', '', '', '', '[]')`,
		base.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO archive_messages(
			conversation_id, turn_id, seq, role, content_json, created_at)
		VALUES ('s-old', ?, 0, 'user', ?, ?)`,
		turnID, string(content), base.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	// Nothing is searchable before the backfill ran.
	if hits, err := s.SearchMessages(ctx, "跨会话", state.SearchOptions{}); err != nil {
		t.Fatal(err)
	} else if len(hits.Hits) != 0 {
		t.Fatalf("unbackfilled hits = %+v, want none", hits.Hits)
	}

	// The migration path: Workspace runs the schema, the legacy import
	// and then the recorded backfill through the importer port.
	if err := compat.Workspace(ctx, s.Handle(), root, state.Importer(s.Handle())); err != nil {
		t.Fatalf("compat.Workspace: %v", err)
	}
	res2, err := s.SearchMessages(ctx, "跨会话", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Hits) != 1 || res2.Hits[0].ConversationID != "s-old" {
		t.Fatalf("backfilled hits = %+v", res2.Hits)
	}

	// Idempotent: a second backfill adds nothing.
	var before int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_fts`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillSearchIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_fts`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("index rows %d -> %d, want no duplicates", before, after)
	}
}

func TestSearchIndexBoundsIndexedText(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	text := strings.Repeat("head-", 1200) + " tailmarker"
	commitTurn(t, s, "s-1", "", "run-1", time.Now().UTC(), userText(text))

	res, err := s.SearchMessages(ctx, "head-", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %+v, want the bounded head to match", res.Hits)
	}
	res, err = s.SearchMessages(ctx, "tailmarker", state.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("hits = %+v, want the text beyond the index cap to be unsearchable", res.Hits)
	}
}
