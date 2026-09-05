package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

func TestHostImportSessionWritesArchiveAndSeedsMemory(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "imported"})
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

	req := ocsessions.ImportRequest{
		Title:  "imported",
		Source: "codex:test-session",
		Turns: []ocsessions.ImportTurn{{
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, "remember me"),
				message.NewTextMessage(message.RoleAssistant, "noted"),
			},
		}},
	}
	id, err := h.ImportSession(ctx, req)
	if err != nil {
		t.Fatalf("ImportSession: %v", err)
	}
	if !strings.HasPrefix(id, "s-") {
		t.Fatalf("imported id %q is not an s- id", id)
	}
	ready, err := h.Sessions().ImportReady(ctx, id)
	if err != nil {
		t.Fatalf("ImportReady: %v", err)
	}
	if !ready {
		t.Fatalf("session %s is not import-ready", id)
	}
	turns, err := h.Sessions().Turns(ctx, id)
	if err != nil {
		t.Fatalf("imported turns: %v", err)
	}
	if len(turns) != 1 || len(turns[0].Messages) != 2 {
		t.Fatalf("imported turns = %+v, want one two-message turn", turns)
	}
	memoryCount := countThreadMemory(t, h, id)
	if memoryCount == 0 {
		t.Fatal("imported session has no memory rows")
	}

	// A duplicate import with the same Source returns the existing
	// session and never seeds memory twice.
	again, err := h.ImportSession(ctx, req)
	if err != nil {
		t.Fatalf("duplicate ImportSession: %v", err)
	}
	if again != id {
		t.Fatalf("duplicate import = %q, want %q", again, id)
	}
	if got := countThreadMemory(t, h, id); got != memoryCount {
		t.Fatalf("memory rows after duplicate import = %d, want %d",
			got, memoryCount)
	}
}

func TestHostImportSessionGeneratesLLMTitle(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "summarized session"})
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

	req := ocsessions.ImportRequest{
		// The bundle title stays as the instant fallback; the
		// asynchronous LLM title replaces the display title.
		Title:  "codex fallback",
		Source: "codex:title-session",
		Turns: []ocsessions.ImportTurn{{
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, "remember me"),
				message.NewTextMessage(message.RoleAssistant, "noted"),
			},
		}},
	}
	id, err := h.ImportSession(ctx, req)
	if err != nil {
		t.Fatalf("ImportSession: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		var title string
		if err := h.Sessions().ReadState(id, "title", &title); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("read auto title: %v", err)
			}
		}
		if title == "summarized session" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto title not generated within deadline (title = %q)", title)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func countThreadMemory(t *testing.T, h *host.Host, id string) int {
	t.Helper()
	var count int
	if err := h.Sessions().Database().SQLDB().QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = ?`,
		id,
	).Scan(&count); err != nil {
		t.Fatalf("count imported memory: %v", err)
	}
	return count
}
