package desktop

import (
	"path/filepath"
	"sync"
	"testing"

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
