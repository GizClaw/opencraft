package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

func patchSummary(t *testing.T, out string) string {
	t.Helper()
	var p Patch
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("decode patch: %v\n%s", err, out)
	}
	text := p.Message.Content.Text()
	if len(text) <= len(summarytext.SummaryPrefix)+1 ||
		text[:len(summarytext.SummaryPrefix)+1] != summarytext.SummaryPrefix+"\n" {
		t.Fatalf("patch message is not marked: %q", text)
	}
	return text[len(summarytext.SummaryPrefix)+1:]
}

// fakeRows is the transcript a fold reads: the tool names a range and loads the
// rows behind it instead of receiving the conversation as an argument.
type fakeRows []summary.StoredMessage

func (f fakeRows) LoadAfter(
	_ context.Context, _ string, afterSeq int64,
) ([]summary.StoredMessage, error) {
	out := make([]summary.StoredMessage, 0, len(f))
	for _, row := range f {
		if row.Seq > afterSeq {
			out = append(out, row)
		}
	}
	return out, nil
}

// rowsOf builds a transcript whose seqs count from 1.
func rowsOf(msgs ...message.Message) fakeRows {
	out := make(fakeRows, 0, len(msgs))
	for i, m := range msgs {
		out = append(out, summary.StoredMessage{
			Seq: int64(i + 1), Role: m.Role, Content: m.Content,
		})
	}
	return out
}

// foldArgs names a range over rows as the tool's input.
func foldArgs(cid string, rows fakeRows, budget int) string {
	return fmt.Sprintf(
		`{"conversation_id":%q,"from_seq":1,"to_seq":%d,"budget_chars":%d}`,
		cid, len(rows), budget)
}

// textMessage is one row of a fixture transcript.
func textMessage(role message.Role, text string) message.Message {
	return message.NewTextMessage(role, text)
}

// TestExecuteShardsOversizedFolds pins the bound on one condensation
// request: a fold larger than maxCondenseChars is condensed in shards and
// merged, so no single provider call carries the whole transcript, while
// every message still ends up covered by the fold.
func TestExecuteShardsOversizedFolds(t *testing.T) {
	// The fold's shards are condensed concurrently, so the probe is
	// deliberately shared across goroutines.
	var (
		calls     atomic.Int64
		maxPrompt atomic.Int64
	)
	tool := &Tool{
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			prompt := req.Input.Content.Text()
			calls.Add(1)
			for {
				prev := maxPrompt.Load()
				if int64(len(prompt)) <= prev ||
					maxPrompt.CompareAndSwap(prev, int64(len(prompt))) {
					break
				}
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant,
					"summary of a shard"),
			}, nil
		},
	}

	// Four messages of ~300 KiB each render to ~1.2 MiB, three times the
	// per-request cap.
	body := strings.Repeat("x", 300<<10)
	rows := rowsOf(
		textMessage(message.RoleUser, body+" one"),
		textMessage(message.RoleAssistant, body+" two"),
		textMessage(message.RoleUser, body+" three"),
		textMessage(message.RoleAssistant, body+" four"),
	)
	tool.rows = rows
	out, err := tool.Execute(context.Background(), foldArgs("s-1", rows, 4096))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if summary := patchSummary(t, out.Text()); summary == "" {
		t.Fatal("fold produced an empty summary")
	}
	if calls.Load() < 2 {
		t.Fatalf("condense calls = %d, want sharded passes", calls.Load())
	}
	// Each request carries one shard; the merge pass carries the partial
	// summaries and stays small. Allow a little headroom for the system
	// instruction and the truncation marker.
	if int(maxPrompt.Load()) > maxCondenseChars+(64<<10) {
		t.Fatalf("largest prompt = %d chars, want <= %d", maxPrompt.Load(),
			maxCondenseChars+(64<<10))
	}
}

// TestExecuteSplitsOversizedMessagesInsteadOfTruncating pins the second
// half of the sharding bound: one message larger than a whole request (a
// tool result carrying a log, a diff, a page of text) used to be cut to the
// cap and sent as one long call. It travels as pieces instead — in
// parallel with the fold's other shards — so no single provider call
// carries more than the cap and the fold keeps the text.
func TestExecuteSplitsOversizedMessagesInsteadOfTruncating(t *testing.T) {
	var (
		calls     atomic.Int64
		promptSum atomic.Int64
		maxPrompt atomic.Int64
	)
	tool := &Tool{
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			prompt := req.Input.Content.Text()
			calls.Add(1)
			promptSum.Add(int64(len(prompt)))
			for {
				prev := maxPrompt.Load()
				if int64(len(prompt)) <= prev ||
					maxPrompt.CompareAndSwap(prev, int64(len(prompt))) {
					break
				}
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant,
					"summary of a piece"),
			}, nil
		},
	}

	// One message three times the per-request cap: nothing else can be
	// split, so every request has to come from this message.
	body := strings.Repeat("x", 1200<<10)
	rows := rowsOf(textMessage(message.RoleUser, body))
	tool.rows = rows
	out, err := tool.Execute(context.Background(), foldArgs("s-1", rows, 4096))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if summary := patchSummary(t, out.Text()); summary == "" {
		t.Fatal("fold produced an empty summary")
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("condense calls = %d, want the message split across requests",
			got)
	}
	if got := promptSum.Load(); got < 1<<20 {
		t.Fatalf(
			"requests carried %d chars of a %d-char message, want the text kept",
			got, len(body))
	}
	if got := int(maxPrompt.Load()); got > maxCondenseChars+(64<<10) {
		t.Fatalf("largest prompt = %d chars, want <= %d", got,
			maxCondenseChars+(64<<10))
	}
}

// TestExecuteCarriesTheMarkedSummaryAsThePreviousSummary verifies the fold
// keeps no state of its own: the summary an earlier fold left arrives inside
// the conversation (the replay hands the condensed view back), so it is
// carried as the previous summary — never condensed as if it were a message,
// and never handed to the model with its rendering marker attached.
func TestExecuteCarriesTheMarkedSummaryAsThePreviousSummary(t *testing.T) {
	var calls int
	tool := &Tool{
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			text := req.Input.Content.Text()
			calls++
			if strings.Contains(text, summarytext.SummaryPrefix) {
				t.Errorf("condense input leaked the summary marker: %q", text)
			}
			if !strings.Contains(text, "S1") {
				t.Errorf("condense input must carry the previous summary: %q", text)
			}
			if !strings.Contains(text, "m3") {
				t.Errorf("condense input must carry the fresh message: %q", text)
			}
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S2"),
			}, nil
		},
	}
	ctx := context.Background()
	marked := summarytext.SummaryPrefix + "\nS1"
	rows := rowsOf(textMessage(message.RoleUser, "m3"))
	tool.rows = rows
	args := fmt.Sprintf(
		`{"conversation_id":"c1","from_seq":1,"to_seq":%d,"budget_chars":100,"prev_summary":%q}`,
		len(rows), marked)
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := patchSummary(t, out.Text()); got != "S2" || calls != 1 {
		t.Fatalf("summary = %q calls=%d, want S2 once", got, calls)
	}

	// Nothing but the summary: the condensed view is already the answer, so
	// there is nothing to fold and no model call to make.
	only := fmt.Sprintf(
		`{"conversation_id":"c1","from_seq":1,"to_seq":1,"budget_chars":100,"prev_summary":%q}`,
		marked)
	tool.rows = fakeRows{}
	out, err = tool.Execute(ctx, only)
	if err != nil {
		t.Fatalf("execute summary-only: %v", err)
	}
	if got := patchSummary(t, out.Text()); got != "S1" || calls != 1 {
		t.Fatalf("summary-only = %q calls=%d, want S1 without a call", got, calls)
	}
}

// TestExecuteRendersToolActivity verifies that tool calls and results
// carried in the folded messages reach the condensation prompt: they
// are rendered as tool_call / tool_result text lines instead of being
// silently dropped.
func TestExecuteRendersToolActivity(t *testing.T) {
	var got string
	tool := &Tool{
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			got = req.Input.Content.Text()
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S1"),
			}, nil
		},
	}
	rows := rowsOf(
		message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "exec_command",
					Arguments: json.RawMessage(`{"cmd":"go test ./..."}`),
				}},
			}},
		},
		message.Message{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("build ok"),
				}},
			}},
		},
	)
	tool.rows = rows
	if _, err := tool.Execute(
		context.Background(), foldArgs("s-1", rows, 100),
	); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(got, `tool_call: exec_command {"cmd":"go test ./..."}`) {
		t.Errorf("condense input missing tool call rendering: %q", got)
	}
	if !strings.Contains(got, "tool_result: build ok") {
		t.Errorf("condense input missing tool result rendering: %q", got)
	}
}

// TestRenderSystemPrompt verifies the embedded template renders the
// handoff instruction as the system message.
func TestRenderSystemPrompt(t *testing.T) {
	got, err := renderSystemPrompt()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "CONTEXT CHECKPOINT COMPACTION") {
		t.Errorf("prompt missing instruction: %q", got)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("prompt must not carry template placeholders: %q", got)
	}
}

func TestExecuteRejectsAnEmptyRange(t *testing.T) {
	tool := &Tool{rows: fakeRows{}}
	if _, err := tool.Execute(context.Background(),
		`{"conversation_id":"c1","from_seq":1,"to_seq":1}`); err == nil {
		t.Fatal("a range with no rows must fail")
	}
	if _, err := tool.Execute(context.Background(),
		`{"conversation_id":"c1","from_seq":0,"to_seq":0}`); err == nil {
		t.Fatal("an inverted range must fail")
	}
	// A deployment without a transcript reader has nothing to fold.
	bare := &Tool{}
	if _, err := bare.Execute(context.Background(),
		`{"conversation_id":"c1","from_seq":1,"to_seq":2}`); err == nil {
		t.Fatal("no reader must fail")
	}
}

// TestFitSummaryKeepsTheArtifactInsideItsBudget pins the accounting rule:
// the identifier block is reserved out of the node's budget instead of
// appended on top of it. The artifact becomes the previous summary of the
// next fold, so an unbudgeted append would grow the conversation's
// compaction state on every fold.
func TestFitSummaryKeepsTheArtifactInsideItsBudget(t *testing.T) {
	anchors := "## Identifiers (extracted verbatim from the folded messages)\n" +
		"Files: internal/a.go, internal/b.go\nCommits: 4f2a1bc"
	prose := strings.Repeat("long prose ", 200) // 2200 runes

	got := fitSummary(prose, anchors, 512)
	if n := utf8.RuneCountInString(got); n > 512 {
		t.Fatalf("fitted summary = %d runes, want at most the budget 512", n)
	}
	if !strings.Contains(got, "Files: internal/a.go") {
		t.Fatalf("the identifier block must survive budgeting:\n%s", got)
	}

	// Without anchors the prose simply takes the whole budget.
	plain := fitSummary(prose, "", 100)
	if n := utf8.RuneCountInString(plain); n != 100 {
		t.Fatalf("plain summary = %d runes, want exactly the budget 100", n)
	}
	if !strings.HasPrefix(plain, "long prose ") {
		t.Fatalf("plain summary was rewritten: %q", plain)
	}

	// A budget too small for even the block still wins: it is the cap the
	// graph asked for, and the block is ordered (files first).
	tiny := fitSummary(prose, anchors, 40)
	if n := utf8.RuneCountInString(tiny); n > 40 {
		t.Fatalf("tiny-budget summary = %d runes, want at most 40", n)
	}
	if !strings.HasPrefix(tiny, "## Identifiers") {
		t.Fatalf("tiny-budget summary = %q, want the head of the block", tiny)
	}

	// Prose that fits alongside the block is left alone.
	short := fitSummary("small summary", anchors, 4096)
	if !strings.HasPrefix(short, "small summary\n\n## Identifiers") {
		t.Fatalf("fitted summary = %q, want prose then the block", short)
	}
}

// TestTruncateRunesNeverSplitsARune pins the encoding rule: the summary is
// re-tokenized by a provider, so a multi-byte character must not be cut in
// half.
func TestTruncateRunesNeverSplitsARune(t *testing.T) {
	cn := "中文摘要内容"
	got := truncateRunes(cn, 3)
	if got != "中文摘" {
		t.Fatalf("truncateRunes = %q, want three runes", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncateRunes produced invalid UTF-8: %q", got)
	}
	if truncateRunes(cn, 0) != "" || truncateRunes(cn, -1) != "" {
		t.Fatal("a non-positive budget must truncate to nothing")
	}
	if truncateRunes(cn, 99) != cn {
		t.Fatal("a budget past the input must return it unchanged")
	}
}

// TestExecuteFoldsFromTheFirstRow pins the boundary the replay actually
// produces: a transcript's first row is seq 0, so a fold of the whole
// conversation starts at 0. Treating that as "no range" is what made every
// long conversation fail to fold and overflow the model window instead.
func TestExecuteFoldsFromTheFirstRow(t *testing.T) {
	var calls int
	tool := &Tool{
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			calls++
			return inference.GenerateResponse{
				Message: message.NewTextMessage(message.RoleAssistant, "S1"),
			}, nil
		},
	}
	rows := fakeRows{{Seq: 0, Role: message.RoleUser, Content: message.NewTextContent("first")},
		{Seq: 1, Role: message.RoleAssistant, Content: message.NewTextContent("reply")}}
	tool.rows = rows
	out, err := tool.Execute(context.Background(),
		`{"conversation_id":"c1","from_seq":0,"to_seq":1,"budget_chars":100}`)
	if err != nil {
		t.Fatalf("execute from seq 0: %v", err)
	}
	if got := patchSummary(t, out.Text()); got != "S1" || calls != 1 {
		t.Fatalf("summary = %q calls=%d, want S1 once", got, calls)
	}
}
