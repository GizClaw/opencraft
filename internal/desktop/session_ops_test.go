package desktop

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/opencraft/internal/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func newSessionOpsApp(t *testing.T) (*App, *ocsessions.Store) {
	t.Helper()
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	a := &App{
		mu:             sync.Mutex{},
		sessions:       store,
		conversationID: "s-current",
		rollouts:       map[string]*rollout.Recorder{},
	}
	return a, store
}

func TestRenameSessionValidatesAndPersists(t *testing.T) {
	a, store := newSessionOpsApp(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RenameSession(id, "  My title  "); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	var got string
	err = store.ReadState(id, "title", &got)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got != "My title" {
		t.Fatalf("title = %q, want trimmed", got)
	}
	if err := a.RenameSession("bad-id", "x"); err == nil {
		t.Fatal("invalid session id accepted")
	}
	if err := a.RenameSession(id, "   "); err == nil {
		t.Fatal("blank title accepted")
	}
}

func TestDeleteSessionRemovesStoreAndRejectsActive(t *testing.T) {
	a, store := newSessionOpsApp(t)
	other, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteSession(other); err != nil {
		t.Fatalf("DeleteSession(other): %v", err)
	}
	if store.Exists(other) {
		t.Fatal("deleted session still exists")
	}
	if err := a.DeleteSession(a.conversationID); err == nil {
		t.Fatal("deleting the active conversation must be rejected")
	}
	if err := a.DeleteSession("bad-id"); err == nil {
		t.Fatal("invalid session id accepted")
	}
}
