package compat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestToolResultContentPartsMigration015 verifies the flat string tool
// results written before flowcraft core v0.4.0 become canonical parts
// in both persisted copies of a message: the archive the chat resumes
// from and the memory window. A string content is what made a whole
// conversation undecodable ("state: decode archive message"), so the
// decode itself is the assertion that matters; call ids, error flags,
// part order and rows that already carry parts must survive unchanged.
func TestToolResultContentPartsMigration015(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	all := workspaceMigrations()
	// Reproduce a pre-015 database: schema only up to migration 014.
	pre015 := make([]db.Migration, 0, len(all))
	for _, m := range all {
		if m.Version <= 14 {
			pre015 = append(pre015, m)
		}
	}
	if err := handle.Migrate(ctx, pre015); err != nil {
		t.Fatalf("apply pre-015 migrations: %v", err)
	}

	const (
		legacyTool = `{"parts":[{"type":"tool_result","result":{` +
			`"call_id":"call-1","content":"{\"exit_code\":0}"}}]}`
		preambleTool = `{"parts":[{"type":"text","text":"preface"},` +
			`{"type":"tool_result","result":{` +
			`"call_id":"call-2","content":"boom","is_error":true}}]}`
		parallelTool = `{"parts":[` +
			`{"type":"tool_result","result":{` +
			`"call_id":"call-4","content":"first"}},` +
			`{"type":"tool_result","result":{` +
			`"call_id":"call-5","content":"second"}}]}`
		canonicalText = `{"parts":[{"type":"text","text":"done"}]}`
		legacyMemory  = `{"parts":[{"type":"tool_result","result":{` +
			`"call_id":"call-3","content":"remembered"}}]}`
		canonicalMemory = `{"parts":[{"type":"text","text":"remembered"}]}`
	)
	for _, stmt := range []string{
		`INSERT INTO archive_messages (
			id, conversation_id, turn_id, seq, role, content_json, created_at
		) VALUES
			(1, 's-1', 1, 1, 'tool', '` + legacyTool + `',
			 '2026-09-10T02:53:57Z'),
			(2, 's-1', 1, 2, 'tool', '` + preambleTool + `',
			 '2026-09-10T02:53:58Z'),
			(3, 's-1', 1, 3, 'tool', '` + parallelTool + `',
			 '2026-09-10T02:53:59Z'),
			(4, 's-1', 1, 4, 'assistant', '` + canonicalText + `',
			 '2026-09-10T02:54:00Z')`,
		`INSERT INTO memory_items (
			id, thread_id, turn_id, seq, item_type, role, payload, created_at
		) VALUES
			('mem-legacy', 's-1', 't-1', 0, 'text', 'tool', '` + legacyMemory + `',
			 '2026-09-10T02:53:57Z'),
			('mem-canonical', 's-1', 't-1', 1, 'text', 'tool',
			 '` + canonicalMemory + `', '2026-09-10T02:53:58Z')`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed legacy rows: %v", err)
		}
	}

	// The shape this step exists for: a string tool result content is
	// not a decodable flowcraft content.
	if err := json.Unmarshal([]byte(legacyTool), &message.Content{}); err == nil {
		t.Fatal("legacy string tool result decoded before migration 015")
	}

	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("apply migration 015: %v", err)
	}
	rewrittenArchive := archiveContents(t, ctx, handle)
	rewrittenMemory := memoryPayloads(t, ctx, handle)
	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("migrations must be idempotent: %v", err)
	}
	for id, want := range rewrittenArchive {
		if got := archiveContents(t, ctx, handle)[id]; got != want {
			t.Fatalf("archive message %d changed on re-run: %s", id, got)
		}
	}
	for id, want := range rewrittenMemory {
		if got := memoryPayloads(t, ctx, handle)[id]; got != want {
			t.Fatalf("memory item %s changed on re-run: %s", id, got)
		}
	}

	single := decodeMessageContent(t, rewrittenArchive[1])
	if len(single.Parts) != 1 {
		t.Fatalf("legacy tool row has %d parts, want 1", len(single.Parts))
	}
	result, ok := single.Parts[0].(message.ToolResultPart)
	if !ok {
		t.Fatalf("legacy tool row part = %T, want message.ToolResultPart",
			single.Parts[0])
	}
	if result.Result.CallID != "call-1" {
		t.Fatalf("call id = %q, want call-1", result.Result.CallID)
	}
	if got := result.Result.Content.Text(); got != `{"exit_code":0}` {
		t.Fatalf("result text = %q, want the original envelope", got)
	}
	if result.Result.IsError {
		t.Fatal("legacy tool row lost its success flag")
	}

	// A message that carried a text preamble keeps its part order and
	// only has the tool result itself rewritten.
	withPreamble := decodeMessageContent(t, rewrittenArchive[2])
	if len(withPreamble.Parts) != 2 {
		t.Fatalf("preamble row has %d parts, want 2", len(withPreamble.Parts))
	}
	text, ok := withPreamble.Parts[0].(message.TextPart)
	if !ok || text.Text != "preface" {
		t.Fatalf("preamble part = %#v, want the text part first", withPreamble.Parts[0])
	}
	result, ok = withPreamble.Parts[1].(message.ToolResultPart)
	if !ok {
		t.Fatalf("preamble row part = %T, want message.ToolResultPart",
			withPreamble.Parts[1])
	}
	if result.Result.CallID != "call-2" || !result.Result.IsError {
		t.Fatalf("preamble tool result = %+v, want call-2 marked as an error",
			result.Result)
	}
	if got := result.Result.Content.Text(); got != "boom" {
		t.Fatalf("preamble result text = %q, want boom", got)
	}

	// One message can carry several tool results; every one of them is
	// rewritten in place, in order.
	parallel := decodeMessageContent(t, rewrittenArchive[3])
	if len(parallel.Parts) != 2 {
		t.Fatalf("parallel row has %d parts, want 2", len(parallel.Parts))
	}
	for i, want := range []struct{ callID, text string }{
		{"call-4", "first"},
		{"call-5", "second"},
	} {
		result, ok := parallel.Parts[i].(message.ToolResultPart)
		if !ok {
			t.Fatalf("parallel row part %d = %T, want message.ToolResultPart",
				i, parallel.Parts[i])
		}
		if result.Result.CallID != want.callID {
			t.Fatalf("parallel row part %d call id = %q, want %q",
				i, result.Result.CallID, want.callID)
		}
		if got := result.Result.Content.Text(); got != want.text {
			t.Fatalf("parallel row part %d text = %q, want %q",
				i, got, want.text)
		}
	}

	if rewrittenArchive[4] != canonicalText {
		t.Fatalf("canonical archive row rewritten: %s", rewrittenArchive[4])
	}
	if rewrittenMemory["mem-canonical"] != canonicalMemory {
		t.Fatalf("canonical memory row rewritten: %s",
			rewrittenMemory["mem-canonical"])
	}
	memory := decodeMessageContent(t, rewrittenMemory["mem-legacy"])
	if len(memory.Parts) != 1 {
		t.Fatalf("legacy memory row has %d parts, want 1", len(memory.Parts))
	}
	result, ok = memory.Parts[0].(message.ToolResultPart)
	if !ok {
		t.Fatalf("legacy memory row part = %T, want message.ToolResultPart",
			memory.Parts[0])
	}
	if result.Result.CallID != "call-3" {
		t.Fatalf("memory call id = %q, want call-3", result.Result.CallID)
	}
	if got := result.Result.Content.Text(); got != "remembered" {
		t.Fatalf("memory result text = %q, want remembered", got)
	}
}

func decodeMessageContent(t *testing.T, raw string) message.Content {
	t.Helper()
	var content message.Content
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatalf("decode content %s: %v", raw, err)
	}
	return content
}

func archiveContents(
	t *testing.T, ctx context.Context, handle *db.DB,
) map[int64]string {
	t.Helper()
	rows, err := handle.SQLDB().QueryContext(ctx,
		`SELECT id, content_json FROM archive_messages ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			t.Fatal(err)
		}
		out[id] = content
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func memoryPayloads(
	t *testing.T, ctx context.Context, handle *db.DB,
) map[string]string {
	t.Helper()
	rows, err := handle.SQLDB().QueryContext(ctx,
		`SELECT id, payload FROM memory_items ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			t.Fatal(err)
		}
		out[id] = payload
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
