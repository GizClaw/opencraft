package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestHostDeleteConversationCancelsLiveRun verifies the desktop
// deletion path: deleting a conversation with a live run cancels the
// run immediately, waits for its terminal persistence, removes the
// stored conversation, and tombstones the id so a stale StartRun can
// never mint the same session again.
func TestHostDeleteConversationCancelsLiveRun(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	gate := provider.HoldNext()
	defer gate.Release()

	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "blocked run"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("provider request did not start")
	}

	// Production callers always wait on the run; deletion must not
	// depend on the order of this goroutine relative to Delete.
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := run.Wait(context.Background())
		waitDone <- waitErr
	}()

	delDone := make(chan error, 1)
	go func() {
		delDone <- h.DeleteConversation(context.Background(), run.ContextID())
	}()
	select {
	case err := <-delDone:
		if err != nil {
			t.Fatalf("DeleteConversation while run active: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("DeleteConversation did not stop the live run in time")
	}

	if h.Sessions().Exists(run.ContextID()) {
		t.Fatal("conversation still exists after delete")
	}
	select {
	case <-waitDone:
	case <-time.After(30 * time.Second):
		t.Fatal("run wait did not settle after deletion")
	}

	// A stale explicit start on the deleted id must be refused, not
	// mint a fresh conversation under the same id.
	if _, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, "again"),
		ContextID: run.ContextID(),
	}); err == nil {
		t.Fatal("StartRun on a deleted session succeeded, want refusal")
	}

	// Deletion is idempotent once complete.
	if err := h.DeleteConversation(context.Background(), run.ContextID()); err != nil {
		t.Fatalf("second DeleteConversation: %v", err)
	}
}

// TestHostDeleteConversationRemovesIdleConversation pins the no-run
// path: an idle conversation's settings rows disappear with the
// delete, and deleting an unknown id stays a no-op.
func TestHostDeleteConversationRemovesIdleConversation(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})

	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	id := sessions.NewID()
	if err := h.Sessions().SetModel(ctx, id, "fake-model"); err != nil {
		t.Fatalf("seed session settings: %v", err)
	}
	if err := h.DeleteConversation(ctx, id); err != nil {
		t.Fatalf("DeleteConversation on idle session: %v", err)
	}
	if h.Sessions().Exists(id) {
		t.Fatal("conversation still exists after delete")
	}
	if model, err := h.Sessions().Model(ctx, id); err != nil || model != "" {
		t.Fatalf("Model after delete = %q, %v; want \"\", nil", model, err)
	}
}
