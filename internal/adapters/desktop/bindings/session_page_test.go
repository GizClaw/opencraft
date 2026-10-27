package bindings

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// One paged read of the archive is bounded: a caller that asks for
// "everything" (limit <= 0, the binding's historical default) gets one
// page, because a long conversation's transcript is tens of megabytes and
// it crosses the desktop bridge twice. The rest is one beforeSeq away.

func TestClampTurnPage(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{in: 0, want: maxTurnPage},
		{in: -5, want: maxTurnPage},
		{in: 1, want: 1},
		{in: 7, want: 7},
		{in: maxTurnPage, want: maxTurnPage},
		{in: maxTurnPage + 1, want: maxTurnPage},
		{in: 5000, want: maxTurnPage},
	} {
		if got := clampTurnPage(tc.in); got != tc.want {
			t.Fatalf("clampTurnPage(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestTurnsReadsOnePageAtMost(t *testing.T) {
	configDir := t.TempDir()
	writeConversationRetryConfig(t, configDir, "")
	home := t.TempDir()
	c := core.NewCore(configDir, t.TempDir(), "")
	ctx := c.Shell.Context()
	c.SetWorkDir(home)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime: %v", err)
	}
	b := NewSessionBinding(c)

	store, release := openWorkspaceSessions(t, c, home)
	defer release()

	// A conversation bigger than one page: seeding through Import writes
	// archived turns without running the engine.
	req := sessions.ImportRequest{Source: "opencraft:test-page", Title: "pages"}
	for i := 0; i < maxTurnPage+10; i++ {
		req.Turns = append(req.Turns, sessions.ImportTurn{
			At: time.Unix(int64(1700000000+i), 0).UTC(),
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, fmt.Sprintf("turn %d", i)),
			},
		})
	}
	id, err := store.Import(ctx, req)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	turns, err := b.Turns(id, 0, 0)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != maxTurnPage {
		t.Fatalf("Turns(limit=0) returned %d turns, want the %d-turn page",
			len(turns), maxTurnPage)
	}

	// A page boundary still pages backwards.
	newest, err := b.Turns(id, 5, 0)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(newest) != 5 {
		t.Fatalf("Turns(limit=5) returned %d turns, want 5", len(newest))
	}
	older, err := b.Turns(id, 5, int64(newest[0].Seq))
	if err != nil {
		t.Fatalf("turns behind the cursor: %v", err)
	}
	if len(older) == 0 || older[len(older)-1].Seq >= newest[0].Seq {
		t.Fatalf("beforeSeq did not page backwards: %d older turns, cursor %d",
			len(older), newest[0].Seq)
	}
}

// The page log is what makes a hydration loop visible: a page that keeps
// arriving for the same conversation while nothing on screen changes shows
// up as its own line, with the size it cost. These cases pin the numbers
// it reports and that it reports them at all.

func TestTurnPageSizeCountsWhatAPageCarries(t *testing.T) {
	call := message.ToolCallPart{Call: message.ToolCall{
		ID:        "call-1",
		Name:      "exec_command",
		Arguments: []byte(`{"command":"ls"}`),
	}}
	result := message.ToolResultPart{Result: message.ToolResult{
		CallID:  "call-1",
		Content: message.NewTextContent("output text"),
	}}
	turns := []SessionTurnDTO{
		{Messages: []message.Message{
			message.NewTextMessage(message.RoleUser, "hello"),
		}},
		{Messages: []message.Message{
			{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{call}}},
			{Role: message.RoleTool, Content: message.Content{Parts: []message.Part{result}}},
		}},
	}
	count, messages, bytes := turnPageSize(turns)
	if count != 2 || messages != 3 {
		t.Fatalf("page = %d turns / %d messages, want 2 / 3", count, messages)
	}
	// "hello" (5) + the tool name and its arguments + the result text.
	want := len("hello") + len("exec_command") + len(`{"command":"ls"}`) + len("output text")
	if bytes != want {
		t.Fatalf("text_bytes = %d, want %d", bytes, want)
	}
}

func TestTurnPageSizeIgnoresNonTextParts(t *testing.T) {
	turns := []SessionTurnDTO{{Messages: []message.Message{
		{Role: message.RoleUser, Content: message.Content{Parts: []message.Part{
			message.FilePart{URI: "file:///tmp/report.pdf"},
		}}},
	}}}
	count, messages, bytes := turnPageSize(turns)
	if count != 1 || messages != 1 || bytes != 0 {
		t.Fatalf("page = %d/%d/%d, want 1 turn, 1 message, 0 bytes",
			count, messages, bytes)
	}
}

func TestTurnsLogsThePageItRead(t *testing.T) {
	recorder := logcapture.Install(t)
	logTurnPage(context.Background(), "turns", "s-1", 7, 0, []SessionTurnDTO{
		{Messages: []message.Message{
			message.NewTextMessage(message.RoleUser, "hello"),
		}},
	})
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want one page log line", len(records))
	}
	if got := records[0].Body().AsString(); got != "session: turns" {
		t.Fatalf("body = %q, want the method named", got)
	}
	for key, want := range map[string]string{
		"conversation.id": "s-1",
		"limit":           "7",
		"before_seq":      "0",
		"turns":           "1",
		"messages":        "1",
		"text_bytes":      "5",
	} {
		if got := logcapture.Attribute(records[0], key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
