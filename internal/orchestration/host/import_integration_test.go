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
	recorded := make(chan ocsessions.Usage, 8)
	mgr.SetUsageRecorder(func(
		_ context.Context,
		_, _ string,
		usage ocsessions.Usage,
	) error {
		recorded <- usage
		return nil
	})
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	finished := at.Add(12 * time.Second)
	wantUsage := ocsessions.Usage{
		Model:            "deepseek-v4-flash",
		InputTokens:      1000,
		OutputTokens:     200,
		TotalTokens:      1200,
		CacheReadTokens:  700,
		CacheWriteTokens: 30,
		ReasoningTokens:  50,
	}
	req := ocsessions.ImportRequest{
		Title:  "imported",
		Source: "codex:test-session",
		Usage:  &wantUsage,
		Turns: []ocsessions.ImportTurn{{
			At:         at,
			FinishedAt: &finished,
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
	if !turns[0].At.Equal(at) ||
		!turns[0].RequestedAt.Equal(at) ||
		!turns[0].StartedAt.Equal(at) ||
		!turns[0].FinishedAt.Equal(finished) {
		t.Fatalf("imported turn timing = %+v", turns[0])
	}
	firstRecorded := receiveUsage(t, recorded)
	if firstRecorded != wantUsage {
		t.Fatalf("first recorded usage = %+v, want %+v", firstRecorded, wantUsage)
	}
	memoryCount := countThreadMemory(t, h, id)
	if memoryCount == 0 {
		t.Fatal("imported session has no memory rows")
	}

	// The asynchronous LLM title also reports usage. Wait for the title
	// (its recorder call completes before the title is written), then
	// drain the remaining deltas so the store snapshot below cannot
	// race the title call.
	waitForImportTitle(t, h, id, "imported")
	wantTotal := wantUsage
	for _, delta := range drainUsageRecorder(recorded) {
		wantTotal = sumUsage(wantTotal, delta)
	}
	snapshot, err := h.Sessions().LoadUsage(ctx, id)
	if err != nil {
		t.Fatalf("LoadUsage snapshot: %v", err)
	}
	if snapshot != wantTotal {
		t.Fatalf("session usage = %+v, want %+v", snapshot, wantTotal)
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
	if len(recorded) != 0 {
		t.Fatalf("duplicate import recorded usage again: %d calls", len(recorded))
	}
	after, err := h.Sessions().LoadUsage(ctx, id)
	if err != nil {
		t.Fatalf("LoadUsage after duplicate: %v", err)
	}
	if after != snapshot {
		t.Fatalf("usage after duplicate = %+v, want snapshot %+v",
			after, snapshot)
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

func waitForImportTitle(t *testing.T, h *host.Host, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var title string
		if err := h.Sessions().ReadState(id, "title", &title); err == nil &&
			title == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto title %q not generated within deadline (got %q)",
				want, title)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func receiveUsage(t *testing.T, ch chan ocsessions.Usage) ocsessions.Usage {
	t.Helper()
	select {
	case usage := <-ch:
		return usage
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for usage recorder call")
		return ocsessions.Usage{}
	}
}

func drainUsageRecorder(ch chan ocsessions.Usage) []ocsessions.Usage {
	var out []ocsessions.Usage
	for {
		select {
		case usage := <-ch:
			out = append(out, usage)
		default:
			return out
		}
	}
}

func sumUsage(total, delta ocsessions.Usage) ocsessions.Usage {
	if delta.Model != "" {
		total.Model = delta.Model
	}
	total.InputTokens += delta.InputTokens
	total.OutputTokens += delta.OutputTokens
	total.TotalTokens += delta.TotalTokens
	total.CacheReadTokens += delta.CacheReadTokens
	total.CacheWriteTokens += delta.CacheWriteTokens
	total.ReasoningTokens += delta.ReasoningTokens
	total.LatencyMs += delta.LatencyMs
	return total
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
