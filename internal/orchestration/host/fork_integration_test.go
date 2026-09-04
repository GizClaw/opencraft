package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

func TestHostForkCopiesArchiveAndSeedsMemory(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "forkable answer"})
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
		Message: message.NewTextMessage(message.RoleUser, "remember me"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("result = %+v, want completed", res)
	}

	forkID, err := h.ForkConversation(ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if forkID == run.ContextID() {
		t.Fatalf("fork reused source id %q", forkID)
	}
	turns, err := h.Sessions().Turns(ctx, forkID)
	if err != nil {
		t.Fatalf("fork turns: %v", err)
	}
	if len(turns) != 1 || turns[0].RunID != run.RunID() {
		t.Fatalf("forked turns = %+v, want one source turn", turns)
	}
	var memoryCount int
	if err := h.Sessions().Database().SQLDB().QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = ?`,
		forkID,
	).Scan(&memoryCount); err != nil {
		t.Fatalf("count fork memory: %v", err)
	}
	if memoryCount == 0 {
		t.Fatal("forked session has no memory rows")
	}
}
