package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func TestResumeSessionAcceptsNewChatBeforePersist(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		mu:       sync.Mutex{},
		sessions: store,
		convRuns: make(map[string]map[string]bool),
	}

	id, err := a.NewChat()
	if err != nil {
		t.Fatal(err)
	}
	// The conversation has no history/usage yet, so store.List() does
	// not know it; ResumeSession must still accept the in-memory id.
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == id {
			t.Fatalf("fresh NewChat conversation leaked into store.List")
		}
	}
	got, err := a.ResumeSession(id)
	if err != nil {
		t.Fatalf("ResumeSession for unpersisted conversation: %v", err)
	}
	if got != id {
		t.Fatalf("ResumeSession returned %q, want %q", got, id)
	}
	if a.conversationID != id {
		t.Fatalf("conversationID = %q, want %q", a.conversationID, id)
	}
}

func TestSessionBindingsRejectTraversalIDs(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		mu:       sync.Mutex{},
		sessions: store,
		workDir:  t.TempDir(),
		convRuns: make(map[string]map[string]bool),
	}
	for _, id := range []string{
		"../escape",
		"../../escape",
		"s-../../../../tmp/x",
	} {
		if _, err := a.ExportSession(id); err == nil {
			t.Fatalf("ExportSession(%q) accepted a traversal id", id)
		}
		if err := a.RenameSession(id, "t"); err == nil {
			t.Fatalf("RenameSession(%q) accepted a traversal id", id)
		}
	}
	// Valid ids still work through the same bindings.
	sid, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RenameSession(sid, "custom"); err != nil {
		t.Fatalf("RenameSession(valid): %v", err)
	}
	if _, err := a.ExportSession(sid); err != nil {
		t.Fatalf("ExportSession(valid): %v", err)
	}
}

func TestExportSessionKeepsUserAndFinalAssistantOnly(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		mu:       sync.Mutex{},
		sessions: store,
		workDir:  t.TempDir(),
		convRuns: make(map[string]map[string]bool),
	}

	sid, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), sid, []message.Message{
		message.NewTextMessage(message.RoleUser, "第一轮"),
		{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ReasoningPart{Text: "内部推理"},
				message.TextPart{Text: "我先看一下"},
			}},
		},
		message.NewTextMessage(message.RoleAssistant, "最终回答一"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), sid, []message.Message{
		message.NewTextMessage(message.RoleUser, "第二轮"),
		message.NewTextMessage(message.RoleAssistant, "最终回答二"),
	}); err != nil {
		t.Fatal(err)
	}

	path, err := a.ExportSession(sid)
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)
	for _, want := range []string{
		"## User",
		"## Assistant",
		"第一轮",
		"第二轮",
		"最终回答一",
		"最终回答二",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("export missing %q:\n%s", want, md)
		}
	}
	for _, banned := range []string{"我先看一下", "内部推理", "## Tool", "tool_call", "tool_result"} {
		if strings.Contains(md, banned) {
			t.Errorf("export contains %q:\n%s", banned, md)
		}
	}
}
